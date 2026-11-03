package main

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Supported file extensions are selected based on common FFmpeg-supported audio
// containers, with some video containers included for audio extraction.
var audioExts = map[string]bool{
	".wav": true, ".wave": true, ".mp3": true, ".m4a": true,
	".aac": true, ".flac": true, ".ogg": true, ".oga": true,
	".opus": true, ".wma": true, ".aiff": true, ".aif": true,
	".aifc": true, ".alac": true, ".ape": true, ".wv": true,
	".caf": true, ".ac3": true, ".mka": true, ".amr": true,
	".mp4": true, ".mov": true, ".mkv": true, ".webm": true,
	".3gp": true, ".3g2": true, ".mp2": true, ".m4b": true,
	".ts": true, ".mts": true, ".m2ts": true, ".pcm": true,
}

func isSupportedAudio(path string) bool {
	return audioExts[strings.ToLower(filepath.Ext(path))]
}

// parsePathInput accepts a single path, quoted Windows Explorer multi-path
// copies, or paths separated by newlines or semicolons.
func parsePathInput(text string) []string {
	var result []string
	var token strings.Builder
	quoted := false
	add := func() {
		s := strings.TrimSpace(token.String())
		token.Reset()
		if s != "" {
			result = append(result, s)
		}
	}
	var prev rune
	for _, r := range text {
		switch {
		case r == '"':
			if quoted {
				quoted = false
				add()
			} else {
				if strings.TrimSpace(token.String()) != "" {
					add()
				}
				quoted = true
			}
		case !quoted && (r == '\n' || r == '\r' || r == ';'):
			add()
		case !quoted && (r == ' ' || r == '\t') && prev == '"':
			// Separator between Explorer's quoted paths.
		default:
			token.WriteRune(r)
		}
		prev = r
	}
	add()
	return result
}

func normalizedPath(path string) string {
	p := filepath.Clean(path)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// collectInputs traverses user-specified folders recursively. Existing
// descendant "outputs" directories are skipped, but an explicitly selected
// outputs directory may still be added as input.
func collectInputs(paths []string, existing []string) ([]string, []string) {
	return collectInputsWithContext(context.Background(), paths, existing)
}

// collectInputsWithContext keeps long/slow folder walks off the Windows GUI
// thread and allows cancellation without waiting for the entire folder tree.
func collectInputsWithContext(ctx context.Context, paths []string, existing []string) ([]string, []string) {
	seen := make(map[string]bool, len(existing)+len(paths))
	for _, f := range existing {
		seen[normalizedPath(f)] = true
	}
	var added, warnings []string
	for _, raw := range paths {
		if ctx.Err() != nil {
			break
		}
		raw = strings.TrimSpace(strings.Trim(raw, "\""))
		if raw == "" {
			continue
		}
		root, err := filepath.Abs(raw)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("パスを解釈できません: %s", raw))
			continue
		}
		info, err := os.Stat(root)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("見つかりません: %s", root))
			continue
		}
		if info.IsDir() {
			err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if walkErr != nil {
					warnings = append(warnings, fmt.Sprintf("読み取り不可: %s (%v)", path, walkErr))
					return nil
				}
				if d.IsDir() {
					if path != root && strings.EqualFold(d.Name(), "outputs") {
						return filepath.SkipDir
					}
					return nil
				}
				if !d.Type().IsRegular() || !isSupportedAudio(path) {
					return nil
				}
				key := normalizedPath(path)
				if !seen[key] {
					added = append(added, path)
					seen[key] = true
				}
				return nil
			})
			if err != nil && ctx.Err() == nil {
				warnings = append(warnings, fmt.Sprintf("フォルダーを走査できません: %s (%v)", root, err))
			}
		} else if isSupportedAudio(root) {
			if !seen[normalizedPath(root)] {
				added = append(added, root)
				seen[normalizedPath(root)] = true
			}
		} else {
			warnings = append(warnings, fmt.Sprintf("対象外の形式: %s", root))
		}
	}
	sort.Strings(added)
	return added, warnings
}

type conversionJob struct {
	Input  string
	Output string
}

var invalidNameChars = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

func validateAffix(s string) error {
	if invalidNameChars.MatchString(s) {
		return fmt.Errorf("接頭辞・接尾辞にファイル名で使えない文字 (\\ / : * ? \" < > |) が含まれています")
	}
	return nil
}

// planJobs allocates collision-free MP3 paths without creating output files.
// When no output directory is specified, each source file's containing
// directory receives an "outputs" subdirectory.
func planJobs(files []string, outputDir, prefix, suffix string) ([]conversionJob, error) {
	if err := validateAffix(prefix); err != nil {
		return nil, err
	}
	if err := validateAffix(suffix); err != nil {
		return nil, err
	}
	if strings.TrimSpace(outputDir) != "" {
		var err error
		outputDir, err = filepath.Abs(strings.TrimSpace(strings.Trim(outputDir, "\"")))
		if err != nil {
			return nil, fmt.Errorf("出力先パスが無効です: %w", err)
		}
		if st, err := os.Stat(outputDir); err == nil && !st.IsDir() {
			return nil, fmt.Errorf("出力先がフォルダーではありません: %s", outputDir)
		}
	}
	used := make(map[string]bool, len(files))
	jobs := make([]conversionJob, 0, len(files))
	for _, file := range files {
		target := outputDir
		if target == "" {
			target = filepath.Join(filepath.Dir(file), "outputs")
		}
		base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		base = prefix + base + suffix
		// Disallow Windows-invalid trailing dots or spaces in composed names.
		if strings.TrimSpace(base) == "" || strings.HasSuffix(base, ".") || strings.HasSuffix(base, " ") {
			return nil, fmt.Errorf("無効な出力ファイル名: %q", base)
		}
		if len([]rune(base)) > 190 {
			return nil, fmt.Errorf("出力ファイル名が長すぎます: %s", base)
		}
		name := base + ".mp3"
		candidate := filepath.Join(target, name)
		for n := 2; ; n++ {
			key := normalizedPath(candidate)
			_, stErr := os.Stat(candidate)
			if !used[key] && os.IsNotExist(stErr) && normalizedPath(candidate) != normalizedPath(file) {
				break
			}
			if stErr != nil && !os.IsNotExist(stErr) {
				return nil, fmt.Errorf("出力先を確認できません: %s: %w", candidate, stErr)
			}
			candidate = filepath.Join(target, fmt.Sprintf("%s_%d.mp3", base, n))
			if n > 999999 {
				return nil, fmt.Errorf("同名ファイルが多すぎます: %s", base)
			}
		}
		used[normalizedPath(candidate)] = true
		jobs = append(jobs, conversionJob{Input: file, Output: candidate})
	}
	return jobs, nil
}

// mode 0: normal stereo average (L+R)/2 (mono input passes through),
// mode 1: unattenuated L+R sum (mono input passes through).
// Bitrate/quality selection matches the GUI combo index.
func convertAudio(ctx context.Context, ffmpegPath string, job conversionJob, quality, mode int) error {
	if err := os.MkdirAll(filepath.Dir(job.Output), 0755); err != nil {
		return fmt.Errorf("出力フォルダー作成失敗: %w", err)
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-n", "-i", job.Input, "-map", "0:a:0", "-vn", "-map_metadata", "0", "-c:a", "libmp3lame"}
	if mode == 1 {
		args = append(args, "-af", "pan=mono|c0=c0+c1")
	}
	args = append(args, "-ac", "1")
	switch quality {
	case 0:
		args = append(args, "-q:a", "0")
	case 1:
		args = append(args, "-q:a", "2")
	case 2:
		args = append(args, "-b:a", "320k")
	case 3:
		args = append(args, "-b:a", "256k")
	case 4:
		args = append(args, "-b:a", "192k")
	case 5:
		args = append(args, "-b:a", "128k")
	default:
		args = append(args, "-q:a", "2")
	}
	// Atomically reserve a new output name. If some other program created the
	// target after planJobs ran, fail rather than overwriting it. Only a file
	// created here is ever removed after a conversion error.
	reserved, err := os.OpenFile(job.Output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
	if err != nil {
		return fmt.Errorf("出力ファイルを新規作成できません（既存ファイルは上書きしません）: %w", err)
	}
	if err := reserved.Close(); err != nil {
		_ = os.Remove(job.Output)
		return err
	}
	args[4] = "-y" // replace our own newly-created empty placeholder
	args = append(args, job.Output)
	cmd := exec.CommandContext(ctx, ffmpegPath, args...)
	hideSubprocessWindow(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(job.Output) // remove partial MP3 on failure or cancellation
		if errors.Is(ctx.Err(), context.Canceled) {
			return context.Canceled
		}
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			detail = err.Error()
		}
		if len(detail) > 650 {
			detail = detail[:650] + "…"
		}
		return fmt.Errorf("FFmpeg: %s", detail)
	}
	return nil
}

func findFFmpeg() string {
	exe, err := os.Executable()
	if err == nil {
		beside := filepath.Join(filepath.Dir(exe), "ffmpeg.exe")
		if st, err := os.Stat(beside); err == nil && !st.IsDir() {
			return beside
		}
	}
	if p, err := ffmpegCachePath(); err == nil {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	for _, candidate := range []string{"ffmpeg.exe", "ffmpeg"} {
		if found, err := exec.LookPath(candidate); err == nil {
			return found
		}
	}
	return ""
}

func ffmpegCachePath() (string, error) {
	p, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(p, "MonoMP3Converter", "ffmpeg.exe"), nil
}

// Download a Windows FFmpeg binary from Gyan's public FFmpeg Essentials build,
// extract only ffmpeg.exe, and install into a per-user cache. The installer
// copies no external FFmpeg binary into this project's distributable archives.
const ffmpegBuildURL = "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"

type progressReader struct {
	reader        io.Reader
	copied, total int64
	lastUpdate    time.Time
	onProgress    func(int64, int64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.copied += int64(n)
	if r.onProgress != nil && (time.Since(r.lastUpdate) > 180*time.Millisecond || err != nil) {
		r.lastUpdate = time.Now()
		r.onProgress(r.copied, r.total)
	}
	return n, err
}

func downloadFFmpeg(ctx context.Context, onProgress func(int64, int64)) (string, error) {
	cachePath, err := ffmpegCachePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return "", err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, ffmpegBuildURL, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("FFmpegダウンロード: HTTP %d", resp.StatusCode)
	}
	ztmp, err := os.CreateTemp(filepath.Dir(cachePath), "ffmpeg-*.zip")
	if err != nil {
		return "", err
	}
	zipPath := ztmp.Name()
	defer os.Remove(zipPath)
	reader := &progressReader{reader: resp.Body, total: resp.ContentLength, onProgress: onProgress}
	_, err = io.Copy(ztmp, reader)
	closeErr := ztmp.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if onProgress != nil {
		onProgress(reader.copied, reader.total)
	}

	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("ZIPの読み込み失敗: %w", err)
	}
	defer archive.Close()
	var ffmpegFile *zip.File
	for _, f := range archive.File {
		if strings.EqualFold(filepath.Base(f.Name), "ffmpeg.exe") && strings.Contains(strings.ToLower(strings.ReplaceAll(f.Name, "\\", "/")), "/bin/") {
			ffmpegFile = f
			break
		}
	}
	if ffmpegFile == nil {
		return "", errors.New("ZIP内にffmpeg.exeが見つかりません")
	}
	input, err := ffmpegFile.Open()
	if err != nil {
		return "", err
	}
	defer input.Close()
	stage := cachePath + ".download"
	out, err := os.Create(stage)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, input)
	closeErr = out.Close()
	if err != nil || closeErr != nil || ctx.Err() != nil {
		_ = os.Remove(stage)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			return "", err
		}
		return "", closeErr
	}
	// Windows may not allow replacement of an open exe. Leave the preexisting
	// binary intact and remove a duplicate staged download in that case.
	if err = os.Rename(stage, cachePath); err != nil {
		_ = os.Remove(stage)
		if _, statErr := os.Stat(cachePath); statErr == nil {
			return cachePath, nil
		}
		return "", err
	}
	return cachePath, nil
}
