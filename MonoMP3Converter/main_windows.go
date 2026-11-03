//go:build windows

package main

// Mono MP3 Converter: a dependency-free Win32 GUI implemented with Go's
// standard library. Windows 10/11 x64, no Go installation needed for EXE.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	wsChild            = 0x40000000
	wsTabStop          = 0x00010000
	wsVScroll          = 0x00200000
	wsHScroll          = 0x00100000
	wsBorder           = 0x00800000
	wsExClientEdge     = 0x00000200

	ssLeft              = 0x00000000
	esAutoHScroll       = 0x0080
	esMultiline         = 0x0004
	esAutoVScroll       = 0x0040
	esReadOnly          = 0x0800
	lbsExtendedSel      = 0x0800
	lbsNoIntegralHeight = 0x0100
	cbsDropDownList     = 0x0003

	wmCreate              = 0x0001
	wmDestroy             = 0x0002
	wmSize                = 0x0005
	wmClose               = 0x0010
	wmGetMinMaxInfo       = 0x0024
	wmSetFont             = 0x0030
	wmCommand             = 0x0111
	wmDropFiles           = 0x0233
	wmAppEvent            = 0x8001
	wmSetRedraw           = 0x000B
	wmUser                = 0x0400
	lbAddString           = 0x0180
	lbResetContent        = 0x0184
	lbGetSelCount         = 0x0190
	lbGetSelItems         = 0x0191
	lbSetHorizontalExtent = 0x0194
	cbAddString           = 0x0143
	cbGetCurSel           = 0x0147
	cbSetCurSel           = 0x014E
	emSetSel              = 0x00B1
	emReplaceSel          = 0x00C2
	pbmSetPos             = wmUser + 2
	pbmSetRange32         = wmUser + 6

	idFiles        = 201
	idAddFiles     = 202
	idAddFolder    = 203
	idRemove       = 204
	idClear        = 205
	idPath         = 206
	idAddPath      = 207
	idOutput       = 208
	idOutputBrowse = 209
	idPrefix       = 210
	idSuffix       = 211
	idQuality      = 212
	idMix          = 213
	idStart        = 214
	idCancel       = 215

	mbOK              = 0x0000
	mbYesNo           = 0x0004
	mbIconError       = 0x0010
	mbIconQuestion    = 0x0020
	mbIconInformation = 0x0040
	idYes             = 6
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	procRegisterClassExW     = user32.NewProc("RegisterClassExW")
	procCreateWindowExW      = user32.NewProc("CreateWindowExW")
	procDefWindowProcW       = user32.NewProc("DefWindowProcW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procSendMessageW         = user32.NewProc("SendMessageW")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procMoveWindow           = user32.NewProc("MoveWindow")
	procGetClientRect        = user32.NewProc("GetClientRect")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procSetWindowTextW       = user32.NewProc("SetWindowTextW")
	procEnableWindow         = user32.NewProc("EnableWindow")
	procShowWindow           = user32.NewProc("ShowWindow")
	procUpdateWindow         = user32.NewProc("UpdateWindow")
	procInvalidateRect       = user32.NewProc("InvalidateRect")
	procGetMessageW          = user32.NewProc("GetMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessageW     = user32.NewProc("DispatchMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procLoadCursorW          = user32.NewProc("LoadCursorW")
	procSetWindowLongPtrW    = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProcW      = user32.NewProc("CallWindowProcW")
	procSetFocus             = user32.NewProc("SetFocus")
	procCreateFontW          = gdi32.NewProc("CreateFontW")
	procDeleteObject         = gdi32.NewProc("DeleteObject")
	procGetModuleHandleW     = kernel32.NewProc("GetModuleHandleW")
	procDragAcceptFiles      = shell32.NewProc("DragAcceptFiles")
	procDragQueryFileW       = shell32.NewProc("DragQueryFileW")
	procDragFinish           = shell32.NewProc("DragFinish")
	procGetOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	procOleInitialize        = ole32.NewProc("OleInitialize")
	procOleUninitialize      = ole32.NewProc("OleUninitialize")
	procInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")

	globalApp        *app
	wndProcCallback  = syscall.NewCallback(wndProc)
	listProcCallback = syscall.NewCallback(listWndProc)
	previousListProc uintptr
)

type windowClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	SmallIcon  uintptr
}

type rect struct{ Left, Top, Right, Bottom int32 }
type point struct{ X, Y int32 }
type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}
type minMaxInfo struct {
	Reserved     point
	MaxSize      point
	MaxPosition  point
	MinTrackSize point
	MaxTrackSize point
}
type initCommonControlsEx struct{ Size, Classes uint32 }

type openFileName struct {
	Size            uint32
	Owner           uintptr
	Instance        uintptr
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustomFilter uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	FileExtension   uint16
	DefExt          *uint16
	CustData        uintptr
	Hook            uintptr
	TemplateName    *uint16
	Reserved        uintptr
	ReservedDW      uint32
	FlagsEx         uint32
}
type browseInfo struct {
	Owner       uintptr
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Callback    uintptr
	Param       uintptr
	Image       int32
}

type uiEvent struct {
	Kind     string
	Text     string
	Percent  int
	Done     int
	Total    int
	Success  int
	Failed   int
	Files    []string
	Warnings []string
	Canceled bool
}

type app struct {
	hwnd       uintptr
	font       uintptr
	controls   map[int]uintptr
	files      []string
	labels     map[string]uintptr
	logBox     uintptr
	progress   uintptr
	running    bool
	cancel     context.CancelFunc
	scanning   bool
	cancelScan context.CancelFunc
	scanQueue  [][]string
	lock       sync.Mutex
	events     []uiEvent
}

func wstr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}
func send(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	r, _, _ := procSendMessageW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}
func setText(hwnd uintptr, value string) {
	procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(wstr(value))))
}
func getText(hwnd uintptr) string {
	count, _, _ := procGetWindowTextLengthW.Call(hwnd)
	buffer := make([]uint16, count+2)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	return syscall.UTF16ToString(buffer)
}
func messageBox(hwnd uintptr, text, title string, flags uintptr) uintptr {
	v, _, _ := procMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(wstr(text))), uintptr(unsafe.Pointer(wstr(title))), flags)
	return v
}
func move(hwnd uintptr, x, y, width, height int) {
	if hwnd == 0 {
		return
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	procMoveWindow.Call(hwnd, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1)
}
func (a *app) control(id int) uintptr { return a.controls[id] }
func (a *app) newControl(id int, cls, caption string, style, exstyle uint32) uintptr {
	wnd, _, _ := procCreateWindowExW.Call(
		uintptr(exstyle), uintptr(unsafe.Pointer(wstr(cls))), uintptr(unsafe.Pointer(wstr(caption))),
		uintptr(style|wsChild|wsVisible), 0, 0, 100, 24,
		a.hwnd, uintptr(id), 0, 0,
	)
	a.controls[id] = wnd
	if a.font != 0 {
		send(wnd, wmSetFont, a.font, 1)
	}
	return wnd
}
func (a *app) newLabel(name, value string) uintptr {
	hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(wstr("STATIC"))), uintptr(unsafe.Pointer(wstr(value))),
		uintptr(wsChild|wsVisible|ssLeft), 0, 0, 100, 22, a.hwnd, 0, 0, 0)
	a.labels[name] = hwnd
	if a.font != 0 {
		send(hwnd, wmSetFont, a.font, 1)
	}
	return hwnd
}
func (a *app) configureUI() {
	a.controls = make(map[int]uintptr)
	a.labels = make(map[string]uintptr)
	face := wstr("Yu Gothic UI")
	height := int32(-18)
	a.font, _, _ = procCreateFontW.Call(uintptr(height), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(face)))
	if a.font == 0 {
		a.font = 0
	}

	a.newLabel("title", "Mono MP3 Converter  |  L + R → Mono")
	a.newLabel("intro", "WAV / MP3 / M4A / FLAC などに対応。ファイルやフォルダーを下の一覧にドロップしてください。")
	a.newLabel("input", "入力ファイル（フォルダー内は再帰検索）")
	list := a.newControl(idFiles, "LISTBOX", "", wsVScroll|wsHScroll|wsTabStop|lbsExtendedSel|lbsNoIntegralHeight, wsExClientEdge)
	previousListProc, _, _ = procSetWindowLongPtrW.Call(list, ^uintptr(3), listProcCallback)
	procDragAcceptFiles.Call(list, 1)
	procDragAcceptFiles.Call(a.hwnd, 1)
	a.newControl(idAddFiles, "BUTTON", "ファイル追加...", wsTabStop, 0)
	a.newControl(idAddFolder, "BUTTON", "フォルダー追加...", wsTabStop, 0)
	a.newControl(idRemove, "BUTTON", "選択した項目を削除", wsTabStop, 0)
	a.newControl(idClear, "BUTTON", "一覧を消去", wsTabStop, 0)
	a.newLabel("count", "0 件")
	a.newLabel("path", "ファイル・フォルダーのパスを直接指定（複数は ; で区切る）")
	a.newControl(idPath, "EDIT", "", wsTabStop|esAutoHScroll, wsExClientEdge)
	a.newControl(idAddPath, "BUTTON", "パスを追加", wsTabStop, 0)
	a.newLabel("output", "出力先フォルダー（空欄なら自動作成）")
	a.newControl(idOutput, "EDIT", "", wsTabStop|esAutoHScroll, wsExClientEdge)
	a.newControl(idOutputBrowse, "BUTTON", "参照...", wsTabStop, 0)
	a.newLabel("outputHint", "未指定 → 各元ファイルのフォルダー内に outputs を作成して保存")
	a.newLabel("prefix", "ファイル名の接頭辞")
	a.newControl(idPrefix, "EDIT", "", wsTabStop|esAutoHScroll, wsExClientEdge)
	a.newLabel("suffix", "ファイル名の接尾辞")
	a.newControl(idSuffix, "EDIT", "", wsTabStop|esAutoHScroll, wsExClientEdge)
	a.newLabel("quality", "MP3品質")
	quality := a.newControl(idQuality, "COMBOBOX", "", wsTabStop|wsVScroll|cbsDropDownList, 0)
	for _, name := range []string{
		"VBR V0（最高品質）", "VBR V2（推奨）", "CBR 320 kbps", "CBR 256 kbps", "CBR 192 kbps", "CBR 128 kbps",
	} {
		send(quality, cbAddString, 0, uintptr(unsafe.Pointer(wstr(name))))
	}
	send(quality, cbSetCurSel, 1, 0)
	a.newLabel("mix", "モノラル合成")
	mix := a.newControl(idMix, "COMBOBOX", "", wsTabStop|wsVScroll|cbsDropDownList, 0)
	for _, name := range []string{
		"(L+R) / 2  - 通常のモノラル化", "L+R 等倍加算  - 音割れ注意",
	} {
		send(mix, cbAddString, 0, uintptr(unsafe.Pointer(wstr(name))))
	}
	send(mix, cbSetCurSel, 0, 0)
	a.newLabel("log", "処理ログ")
	a.logBox, _, _ = procCreateWindowExW.Call(wsExClientEdge, uintptr(unsafe.Pointer(wstr("EDIT"))), uintptr(unsafe.Pointer(wstr(""))),
		wsChild|wsVisible|wsVScroll|esMultiline|esAutoVScroll|esReadOnly,
		0, 0, 100, 100, a.hwnd, 0, 0, 0)
	if a.font != 0 {
		send(a.logBox, wmSetFont, a.font, 1)
	}
	a.newLabel("status", "準備完了。初回にFFmpegが見つからない場合は自動ダウンロードできます。")
	a.progress, _, _ = procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(wstr("msctls_progress32"))), 0,
		wsChild|wsVisible, 0, 0, 100, 20, a.hwnd, 0, 0, 0)
	send(a.progress, pbmSetRange32, 0, 100)
	a.newControl(idStart, "BUTTON", "MP3に一括変換", wsTabStop, 0)
	a.newControl(idCancel, "BUTTON", "中止", wsTabStop, 0)
	procEnableWindow.Call(a.control(idCancel), 0)
	a.appendLog("ドラッグ＆ドロップ、ファイル参照、フォルダー参照、パス直接指定に対応しています。")
	a.layout()
}

func (a *app) layout() {
	if a.hwnd == 0 {
		return
	}
	var rc rect
	procGetClientRect.Call(a.hwnd, uintptr(unsafe.Pointer(&rc)))
	width, height := int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)
	if width < 300 || height < 300 {
		return
	}
	const margin = 20
	move(a.labels["title"], margin, 13, width-40, 30)
	move(a.labels["intro"], margin, 46, width-40, 23)
	move(a.labels["input"], margin, 75, width-40, 22)
	listTop := 99
	listH := height - 505
	if listH < 152 {
		listH = 152
	}
	buttonWidth := 154
	buttonX := width - margin - buttonWidth
	listW := buttonX - margin - 13
	move(a.control(idFiles), margin, listTop, listW, listH)
	for n, id := range []int{idAddFiles, idAddFolder, idRemove, idClear} {
		move(a.control(id), buttonX, listTop+n*43, buttonWidth, 34)
	}
	move(a.labels["count"], buttonX, listTop+4*43+3, buttonWidth, 20)
	manualTop := listTop + listH + 10
	move(a.labels["path"], margin, manualTop, width-40, 22)
	move(a.control(idPath), margin, manualTop+24, width-169, 28)
	move(a.control(idAddPath), width-138, manualTop+23, 118, 31)

	settingsTop := manualTop + 70
	leftWidth := (width - 62) / 2
	rightX := leftWidth + 42
	rightWidth := width - rightX - margin
	move(a.labels["output"], margin, settingsTop, leftWidth, 22)
	move(a.control(idOutput), margin, settingsTop+24, leftWidth-102, 28)
	move(a.control(idOutputBrowse), margin+leftWidth-95, settingsTop+23, 95, 30)
	move(a.labels["outputHint"], margin, settingsTop+57, leftWidth, 22)
	affixWidth := (leftWidth - 12) / 2
	move(a.labels["prefix"], margin, settingsTop+84, affixWidth, 20)
	move(a.labels["suffix"], margin+affixWidth+12, settingsTop+84, affixWidth, 20)
	move(a.control(idPrefix), margin, settingsTop+105, affixWidth, 28)
	move(a.control(idSuffix), margin+affixWidth+12, settingsTop+105, affixWidth, 28)
	move(a.labels["quality"], margin, settingsTop+146, affixWidth, 20)
	move(a.labels["mix"], margin+affixWidth+12, settingsTop+146, affixWidth, 20)
	// Combo boxes need extra dropdown height even if the visible field is 28px.
	move(a.control(idQuality), margin, settingsTop+169, affixWidth, 220)
	move(a.control(idMix), margin+affixWidth+12, settingsTop+169, affixWidth, 130)

	move(a.labels["log"], rightX, settingsTop, rightWidth, 22)
	logBottom := height - 116
	logTop := settingsTop + 24
	move(a.logBox, rightX, logTop, rightWidth, logBottom-logTop)
	move(a.labels["status"], margin, height-124, width-40, 21)
	move(a.progress, margin, height-101, width-40, 20)
	move(a.control(idStart), width-355, height-71, 220, 36)
	move(a.control(idCancel), width-125, height-71, 105, 36)
}

func (a *app) appendLog(text string) {
	if a.logBox == 0 {
		return
	}
	send(a.logBox, emSetSel, ^uintptr(0), ^uintptr(0))
	send(a.logBox, emReplaceSel, 0, uintptr(unsafe.Pointer(wstr(text+"\r\n"))))
}
func (a *app) setStatus(text string) { setText(a.labels["status"], text) }
func (a *app) setPercent(p int) {
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	send(a.progress, pbmSetPos, uintptr(p), 0)
}

// All GUI controls are updated only from the pinned Windows message thread.
// Limit the visible list to avoid blocking the UI when thousands of files are
// selected. The backing a.files still contains ALL files for conversion.
const visibleFileLimit = 3000

func (a *app) refreshFiles() {
	list := a.control(idFiles)
	send(list, wmSetRedraw, 0, 0)
	send(list, lbResetContent, 0, 0)
	for i, item := range a.files {
		if i >= visibleFileLimit {
			break
		}
		send(list, lbAddString, 0, uintptr(unsafe.Pointer(wstr(item))))
	}
	send(list, lbSetHorizontalExtent, 2500, 0)
	send(list, wmSetRedraw, 1, 0)
	procInvalidateRect.Call(list, 0, 1)
	label := fmt.Sprintf("%d 件", len(a.files))
	if len(a.files) > visibleFileLimit {
		label = fmt.Sprintf("%d 件 (一覧は先頭%d件)", len(a.files), visibleFileLimit)
	}
	setText(a.labels["count"], label)
}

// The picker and drop handlers only enqueue the paths. Slow folder traversal
// runs on a worker goroutine; no recursive disk I/O occurs in a window proc.
func (a *app) addPaths(paths []string) {
	if a.running || len(paths) == 0 {
		return
	}
	a.scanQueue = append(a.scanQueue, append([]string(nil), paths...))
	a.startNextScan()
}
func (a *app) startNextScan() {
	if a.running || a.scanning || len(a.scanQueue) == 0 {
		return
	}
	paths := a.scanQueue[0]
	a.scanQueue = a.scanQueue[1:]
	snapshot := append([]string(nil), a.files...)
	ctx, cancel := context.WithCancel(context.Background())
	a.scanning = true
	a.cancelScan = cancel
	procEnableWindow.Call(a.control(idStart), 0)
	procEnableWindow.Call(a.control(idCancel), 1)
	a.setStatus(fmt.Sprintf("ファイルを検索しています... (%d パス)", len(paths)))
	go func() {
		added, warnings := collectInputsWithContext(ctx, paths, snapshot)
		a.enqueue(uiEvent{Kind: "scan-result", Files: added, Warnings: warnings, Canceled: ctx.Err() != nil})
	}()
}
func (a *app) finishScan(e uiEvent) {
	a.scanning = false
	a.cancelScan = nil
	if e.Canceled {
		a.appendLog("ファイル検索を中止しました。")
	} else {
		// Paths may have been added or removed since this worker started.
		seen := make(map[string]bool, len(a.files)+len(e.Files))
		for _, f := range a.files {
			seen[normalizedPath(f)] = true
		}
		added := 0
		for _, f := range e.Files {
			key := normalizedPath(f)
			if !seen[key] {
				a.files = append(a.files, f)
				seen[key] = true
				added++
			}
		}
		if added > 0 {
			a.refreshFiles()
		}
		a.appendLog(fmt.Sprintf("%d 件を追加しました（合計 %d 件）", added, len(a.files)))
		for i, warn := range e.Warnings {
			if i >= 30 {
				a.appendLog(fmt.Sprintf("その他の注意: %d 件", len(e.Warnings)-i))
				break
			}
			a.appendLog("注意: " + warn)
		}
	}
	if len(a.scanQueue) != 0 {
		a.startNextScan()
	} else {
		procEnableWindow.Call(a.control(idStart), 1)
		procEnableWindow.Call(a.control(idCancel), 0)
		a.setStatus(fmt.Sprintf("検索完了。変換対象: %d 件", len(a.files)))
	}
}
func (a *app) removeSelected() {
	if a.running {
		return
	}
	lb := a.control(idFiles)
	n := int32(send(lb, lbGetSelCount, 0, 0))
	if n <= 0 || int(n) > len(a.files) {
		return
	}
	indices := make([]int32, n)
	count := int(send(lb, lbGetSelItems, uintptr(n), uintptr(unsafe.Pointer(&indices[0]))))
	deleted := make(map[int32]bool, count)
	for i := 0; i < count; i++ {
		deleted[indices[i]] = true
	}
	kept := a.files[:0]
	for i, item := range a.files {
		if !deleted[int32(i)] {
			kept = append(kept, item)
		}
	}
	a.files = kept
	a.refreshFiles()
	a.appendLog(fmt.Sprintf("%d 件を一覧から削除しました。", count))
}
func (a *app) dropFiles(hdrop uintptr) {
	defer procDragFinish.Call(hdrop)
	count, _, _ := procDragQueryFileW.Call(hdrop, ^uintptr(0), 0, 0)
	if count == 0 {
		return
	}
	paths := make([]string, 0, int(count))
	for i := uintptr(0); i < count; i++ {
		n, _, _ := procDragQueryFileW.Call(hdrop, i, 0, 0)
		if n == 0 {
			continue
		}
		buf := make([]uint16, n+1)
		procDragQueryFileW.Call(hdrop, i, uintptr(unsafe.Pointer(&buf[0])), n+1)
		paths = append(paths, syscall.UTF16ToString(buf))
	}
	a.addPaths(paths)
}
func (a *app) browseFiles() {
	paths, err := a.modernPicker(false)
	if err != nil {
		messageBox(a.hwnd, err.Error(), "ファイル選択エラー", mbOK|mbIconError)
		return
	}
	a.addPaths(paths)
}
func (a *app) browseFolder() string {
	paths, err := a.modernPicker(true)
	if err != nil {
		messageBox(a.hwnd, err.Error(), "フォルダー選択エラー", mbOK|mbIconError)
		return ""
	}
	if len(paths) == 0 {
		return ""
	}
	return paths[0]
}
func (a *app) enableRunning(running bool) {
	a.running = running
	disabled := []int{idFiles, idAddFiles, idAddFolder, idRemove, idClear, idPath, idAddPath, idOutput, idOutputBrowse, idPrefix, idSuffix, idQuality, idMix, idStart}
	for _, id := range disabled {
		v := uintptr(1)
		if running {
			v = 0
		}
		procEnableWindow.Call(a.control(id), v)
	}
	v := uintptr(0)
	if running {
		v = 1
	}
	procEnableWindow.Call(a.control(idCancel), v)
}
func (a *app) enqueue(event uiEvent) {
	a.lock.Lock()
	a.events = append(a.events, event)
	a.lock.Unlock()
	procPostMessageW.Call(a.hwnd, wmAppEvent, 0, 0)
}
func (a *app) flushEvents() {
	a.lock.Lock()
	list := a.events
	a.events = nil
	a.lock.Unlock()
	for _, e := range list {
		switch e.Kind {
		case "log":
			a.appendLog(e.Text)
		case "status":
			a.setStatus(e.Text)
		case "percent":
			a.setPercent(e.Percent)
		case "scan-result":
			a.finishScan(e)
		case "end":
			a.enableRunning(false)
			a.setPercent(100 * e.Done / maxInt(e.Total, 1))
			a.setStatus(e.Text)
			a.appendLog(e.Text)
			a.cancel = nil
			messageBox(a.hwnd, e.Text, "変換終了", mbOK|mbIconInformation)
		}
	}
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func (a *app) convertAll() {
	if a.running || a.scanning || len(a.scanQueue) > 0 {
		return
	}
	if len(a.files) == 0 {
		messageBox(a.hwnd, "入力ファイルを追加してください。", "入力なし", mbOK|mbIconInformation)
		return
	}
	target := strings.TrimSpace(getText(a.control(idOutput)))
	prefix := getText(a.control(idPrefix))
	suffix := getText(a.control(idSuffix))
	jobs, err := planJobs(a.files, target, prefix, suffix)
	if err != nil {
		messageBox(a.hwnd, err.Error(), "設定エラー", mbOK|mbIconError)
		return
	}
	quality := int(send(a.control(idQuality), cbGetCurSel, 0, 0))
	mix := int(send(a.control(idMix), cbGetCurSel, 0, 0))
	ffmpegPath := findFFmpeg()
	needDownload := ffmpegPath == ""
	if needDownload {
		response := messageBox(a.hwnd,
			"音声変換に必要なFFmpegが見つかりません。\n\n公開配布されているWindows用FFmpegを初回のみダウンロードしますか？\n（通信量は約100 MB程度。ダウンロード先: ユーザーのキャッシュフォルダー）\n\n『いいえ』の場合は ffmpeg.exe を本アプリの隣に置いてから再実行してください。",
			"FFmpegが必要です", mbYesNo|mbIconQuestion)
		if response != idYes {
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.enableRunning(true)
	a.setPercent(0)
	a.setStatus(fmt.Sprintf("処理準備中: %d 件", len(jobs)))
	a.appendLog(fmt.Sprintf("--- 一括変換開始: %d 件 / 品質: %d / 合成: %d ---", len(jobs), quality, mix))
	go func() {
		if needDownload {
			a.enqueue(uiEvent{Kind: "status", Text: "FFmpegをダウンロードしています..."})
			a.enqueue(uiEvent{Kind: "log", Text: "FFmpeg Essentials（gyan.dev）をダウンロードしています..."})
			path, err := downloadFFmpeg(ctx, func(done, total int64) {
				if total > 0 {
					pct := int(done * 100 / total)
					a.enqueue(uiEvent{Kind: "percent", Percent: pct})
					a.enqueue(uiEvent{Kind: "status", Text: fmt.Sprintf("FFmpegダウンロード中: %d%%", pct)})
				} else {
					a.enqueue(uiEvent{Kind: "status", Text: fmt.Sprintf("FFmpegダウンロード中: %d MB", done/(1024*1024))})
				}
			})
			if err != nil {
				label := "FFmpegの取得に失敗しました: " + err.Error()
				if ctx.Err() != nil {
					label = "ユーザー操作により中止しました。"
				}
				a.enqueue(uiEvent{Kind: "end", Text: label, Done: 0, Total: len(jobs)})
				return
			}
			ffmpegPath = path
			a.enqueue(uiEvent{Kind: "log", Text: "FFmpegを利用可能にしました: " + path})
		}
		success, failed, done := 0, 0, 0
		for i, job := range jobs {
			if ctx.Err() != nil {
				break
			}
			a.enqueue(uiEvent{Kind: "status", Text: fmt.Sprintf("変換中 %d/%d: %s", i+1, len(jobs), filepath.Base(job.Input))})
			a.enqueue(uiEvent{Kind: "log", Text: fmt.Sprintf("[%d/%d] %s", i+1, len(jobs), job.Input)})
			err := convertAudio(ctx, ffmpegPath, job, quality, mix)
			if ctx.Err() != nil {
				break
			}
			done++
			if err != nil {
				failed++
				a.enqueue(uiEvent{Kind: "log", Text: "  失敗: " + err.Error()})
			} else {
				success++
				a.enqueue(uiEvent{Kind: "log", Text: "  完了: " + job.Output})
			}
			a.enqueue(uiEvent{Kind: "percent", Percent: done * 100 / len(jobs)})
		}
		message := fmt.Sprintf("変換終了: 成功 %d 件 / 失敗 %d 件 / 全 %d 件", success, failed, len(jobs))
		if ctx.Err() != nil {
			message = fmt.Sprintf("中止しました: 成功 %d 件 / 失敗 %d 件 / 未処理 %d 件", success, failed, len(jobs)-done)
		}
		a.enqueue(uiEvent{Kind: "end", Text: message, Done: done, Total: len(jobs), Success: success, Failed: failed})
	}()
}

func (a *app) command(id int) {
	switch id {
	case idAddFiles:
		a.browseFiles()
	case idAddFolder:
		if path := a.browseFolder(); path != "" {
			a.addPaths([]string{path})
		}
	case idRemove:
		a.removeSelected()
	case idClear:
		if !a.running {
			a.files = nil
			a.refreshFiles()
			a.appendLog("入力一覧を消去しました。")
		}
	case idAddPath:
		paths := parsePathInput(getText(a.control(idPath)))
		a.addPaths(paths)
		if len(paths) > 0 {
			setText(a.control(idPath), "")
		}
	case idOutputBrowse:
		if path := a.browseFolder(); path != "" {
			setText(a.control(idOutput), path)
		}
	case idStart:
		a.convertAll()
	case idCancel:
		if a.scanning {
			a.scanQueue = nil
			if a.cancelScan != nil {
				a.cancelScan()
			}
			a.setStatus("ファイル検索の中止を要求しました...")
			return
		}
		if a.cancel != nil {
			a.cancel()
			a.setStatus("中止を要求しました...")
		}
	}
}
func wndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	a := globalApp
	switch msg {
	case wmCreate:
		a.hwnd = hwnd
		a.configureUI()
		return 0
	case wmSize:
		if a != nil {
			a.layout()
		}
		return 0
	case wmCommand:
		if a != nil && (wparam>>16) == 0 {
			a.command(int(wparam & 0xFFFF))
		}
		return 0
	case wmDropFiles:
		if a != nil {
			a.dropFiles(wparam)
		}
		return 0
	case wmAppEvent:
		if a != nil {
			a.flushEvents()
		}
		return 0
	case wmClose:
		if a != nil && a.scanning && a.cancelScan != nil {
			a.cancelScan()
		}
		if a != nil && a.running {
			if messageBox(hwnd, "処理中です。中止して終了しますか？", "終了確認", mbYesNo|mbIconQuestion) != idYes {
				return 0
			}
			if a.cancel != nil {
				a.cancel()
			}
		}
		procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		if a != nil && a.font != 0 {
			procDeleteObject.Call(a.font)
		}
		procPostQuitMessage.Call(0)
		return 0
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wparam, lparam)
	return result
}
func listWndProc(hwnd uintptr, msg uint32, wparam, lparam uintptr) uintptr {
	if msg == wmDropFiles {
		if globalApp != nil {
			globalApp.dropFiles(wparam)
		}
		return 0
	}
	result, _, _ := procCallWindowProcW.Call(previousListProc, hwnd, uintptr(msg), wparam, lparam)
	return result
}
func main() {
	// Win32 window ownership, message pumping and COM require the same OS
	// thread. Without this, file dialogs and drag-and-drop can hang.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := procOleInitialize.Call(0)
	if uint32(hr)&0x80000000 != 0 {
		messageBox(0, fmt.Sprintf("Windows COM の初期化に失敗しました: 0x%08X", uint32(hr)), "起動エラー", mbOK|mbIconError)
		return
	}
	defer procOleUninitialize.Call()
	icc := initCommonControlsEx{Size: uint32(unsafe.Sizeof(initCommonControlsEx{})), Classes: 0x20}
	procInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))

	instance, _, _ := procGetModuleHandleW.Call(0)
	cursor, _, _ := procLoadCursorW.Call(0, 32512) // IDC_ARROW
	name := wstr("MonoMP3ConverterWin32")
	klass := windowClassEx{Size: uint32(unsafe.Sizeof(windowClassEx{})), WndProc: wndProcCallback,
		Instance: instance, Cursor: cursor, Background: 6, ClassName: name}
	registered, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&klass)))
	if registered == 0 {
		messageBox(0, "ウィンドウの初期化に失敗しました: "+err.Error(), "起動エラー", mbOK|mbIconError)
		return
	}
	globalApp = &app{}
	title := wstr("Mono MP3 Converter - L+R モノラル変換")
	hwnd, _, createErr := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(title)),
		wsOverlappedWindow|wsVisible, 100, 70, 960, 790, 0, 0, instance, 0)
	if hwnd == 0 {
		messageBox(0, "ウィンドウ作成に失敗しました: "+createErr.Error(), "起動エラー", mbOK|mbIconError)
		return
	}
	globalApp.hwnd = hwnd
	// Explorer also supports dropping files/folders onto the .exe icon.
	if len(os.Args) > 1 {
		globalApp.addPaths(os.Args[1:])
	}
	procShowWindow.Call(hwnd, 1)
	procUpdateWindow.Call(hwnd)
	var message winMsg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
}
