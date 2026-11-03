package main

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePathInput(t *testing.T) {
	got := parsePathInput(`"C:\audio files\first.wav" "C:\music\second.mp3";D:\third.m4a`)
	expect := []string{`C:\audio files\first.wav`, `C:\music\second.mp3`, `D:\third.m4a`}
	if len(got) != len(expect) {
		t.Fatalf("want %d paths got %d: %q", len(expect), len(got), got)
	}
	for i := range got {
		if got[i] != expect[i] {
			t.Errorf("path %d: want %q got %q", i, expect[i], got[i])
		}
	}
}

func TestCollectInputsAndPlanJobs(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "song.wav")
	sub := filepath.Join(dir, "Album")
	skip := filepath.Join(sub, "outputs")
	for _, p := range []string{sub, skip} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	two := filepath.Join(sub, "song.mp3")
	for _, p := range []string{one, two, filepath.Join(skip, "old.mp3")} {
		if err := os.WriteFile(p, []byte("test"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	inputs, warnings := collectInputs([]string{dir, one}, nil)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %q", warnings)
	}
	if len(inputs) != 2 {
		t.Fatalf("expected 2 discovered files, got %v", inputs)
	}
	jobs, err := planJobs(inputs, "", "MONO_", "_mix")
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		want := filepath.Join(filepath.Dir(job.Input), "outputs", "MONO_song_mix.mp3")
		if job.Output != want {
			t.Errorf("want %s; got %s", want, job.Output)
		}
	}
	shared := filepath.Join(dir, "all_outputs")
	if err := os.MkdirAll(shared, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "MONO_song_mix.mp3"), []byte("KEEP"), 0644); err != nil {
		t.Fatal(err)
	}
	jobs, err = planJobs(inputs, shared, "MONO_", "_mix")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(jobs[0].Output, "MONO_song_mix_2.mp3") || !strings.HasSuffix(jobs[1].Output, "MONO_song_mix_3.mp3") {
		t.Errorf("wrong collision handling: %+v", jobs)
	}
	if _, err = planJobs(inputs, shared, "bad/", ""); err == nil {
		t.Fatal("invalid prefix should fail")
	}
}

func rmsForMP3(t *testing.T, path string) float64 {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "f32le", "-acodec", "pcm_f32le", "-ac", "1", "-")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 8 {
		t.Fatal("no decoded audio")
	}
	sum := 0.0
	for i := 0; i+4 <= len(out); i += 4 {
		value := float64(math.Float32frombits(binary.LittleEndian.Uint32(out[i:])))
		sum += value * value
	}
	return math.Sqrt(sum / float64(len(out)/4))
}

func TestFFmpegConversionAndMonoSum(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "stereo.wav")
	// Identical 0.2-amplitude sine in left and right channels.
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i",
		"aevalsrc=0.2*sin(2*PI*440*t)|0.2*sin(2*PI*440*t):s=44100:d=0.5", "-c:a", "pcm_s16le", in)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create input: %v: %s", err, b)
	}
	avg := conversionJob{Input: in, Output: filepath.Join(dir, "avg.mp3")}
	sum := conversionJob{Input: in, Output: filepath.Join(dir, "sum.mp3")}
	if err := convertAudio(context.Background(), ffmpeg, avg, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := convertAudio(context.Background(), ffmpeg, sum, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := convertAudio(context.Background(), ffmpeg, avg, 1, 0); err == nil {
		t.Fatal("existing output must not be overwritten")
	}
	rAvg := rmsForMP3(t, avg.Output)
	rSum := rmsForMP3(t, sum.Output)
	ratio := rSum / rAvg
	if ratio < 1.9 || ratio > 2.1 {
		t.Errorf("sum/average energy ratio should be about 2; got %.3f (avg %.3f, sum %.3f)", ratio, rAvg, rSum)
	}
	out, _ := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=channels", "-of", "default=nw=1:nk=1", avg.Output).Output()
	if strings.TrimSpace(string(out)) != "1" {
		t.Errorf("output should be mono channel: %s", out)
	}
}

func TestCollectInputsCancellation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.wav"), []byte("wav"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	added, _ := collectInputsWithContext(ctx, []string{dir}, nil)
	if len(added) != 0 {
		t.Fatalf("expected no files after cancellation: %v", added)
	}
}

func TestFFmpegCommonFormats(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "input.wav")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "aevalsrc=0.15*sin(2*PI*440*t)|0.17*sin(2*PI*880*t):s=44100:d=0.3", "-c:a", "pcm_s16le", src)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v %s", err, b)
	}
	for _, format := range []struct{ ext, codec string }{{"mp3", "libmp3lame"}, {"m4a", "aac"}, {"flac", "flac"}} {
		t.Run(format.ext, func(t *testing.T) {
			in := filepath.Join(dir, "source."+format.ext)
			cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-i", src, "-c:a", format.codec, in)
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("encode: %v %s", err, b)
			}
			jobs, err := planJobs([]string{in}, "", "PRE_", "_MONO")
			if err != nil {
				t.Fatal(err)
			}
			if err := convertAudio(context.Background(), ffmpeg, jobs[0], 1, 0); err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(dir, "outputs", "PRE_source_MONO.mp3")
			if jobs[0].Output != want {
				t.Fatalf("path: got %s, want %s", jobs[0].Output, want)
			}
			channels, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=channels", "-of", "default=nw=1:nk=1", want).Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(channels)) != "1" {
				t.Fatalf("expected mono: %s", channels)
			}
			// Test branches reuse the same output base: clean before next format.
			if err := os.Remove(want); err != nil {
				t.Fatal(err)
			}
		})
	}
}
