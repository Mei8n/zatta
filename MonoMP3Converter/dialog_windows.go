//go:build windows

package main

// Modern native Windows file/folder picker using IFileOpenDialog.
// Unlike SHBrowseForFolder, both modes use the familiar Explorer interface
// with breadcrumb navigation, an address bar, and a resizable window.

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

type comGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidFileOpenDialog  = comGUID{0xdc1c5a9c, 0xe88a, 0x4dde, [8]byte{0xa5, 0xa1, 0x60, 0xf8, 0x2a, 0x20, 0xae, 0xf7}}
	iidFileOpenDialog    = comGUID{0xd57c7288, 0xd4ad, 0x4768, [8]byte{0xbe, 0x02, 0x9d, 0x96, 0x95, 0x32, 0xd9, 0x60}}
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")
)

const (
	fosOverwritePrompt  = 0x00000002
	fosPickFolders      = 0x00000020
	fosForceFileSystem  = 0x00000040
	fosAllowMultiSelect = 0x00000200
	fosPathMustExist    = 0x00000800
	fosFileMustExist    = 0x00001000
	sigdnFileSysPath    = 0x80058000
	hresultCancelled    = 0x800704c7
)

type filterSpec struct {
	Name *uint16
	Spec *uint16
}

func failedHRESULT(hr uintptr) bool { return uint32(hr)&0x80000000 != 0 }

// The vtable indices are those of Microsoft's IFileDialog/IFileOpenDialog,
// IShellItem and IShellItemArray interfaces. All methods use stdcall on Win64.
func invokeCOM(ptr uintptr, index uintptr, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(ptr))
	entry := *(*uintptr)(unsafe.Pointer(vtbl + index*unsafe.Sizeof(uintptr(0))))
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, ptr)
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(entry, all...)
	return r
}

func shellItemPath(item uintptr) (string, error) {
	var display uintptr
	hr := invokeCOM(item, 5, sigdnFileSysPath, uintptr(unsafe.Pointer(&display))) // IShellItem.GetDisplayName
	if failedHRESULT(hr) {
		return "", fmt.Errorf("GetDisplayName: 0x%08X", uint32(hr))
	}
	if display == 0 {
		return "", fmt.Errorf("Windows returned an empty path")
	}
	defer procCoTaskMemFree.Call(display)
	buf := make([]uint16, 0, 260)
	for i := uintptr(0); i < 32768; i++ {
		c := *(*uint16)(unsafe.Pointer(display + i*2))
		if c == 0 {
			break
		}
		buf = append(buf, c)
	}
	return syscall.UTF16ToString(buf), nil
}

func (a *app) modernPicker(folders bool) ([]string, error) {
	var dialog uintptr
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)), 0, 1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&iidFileOpenDialog)), uintptr(unsafe.Pointer(&dialog)),
	)
	if failedHRESULT(hr) || dialog == 0 {
		return nil, fmt.Errorf("ファイル選択ダイアログの初期化に失敗しました (HRESULT 0x%08X)", uint32(hr))
	}
	defer invokeCOM(dialog, 2) // Release

	var flags uint32
	hr = invokeCOM(dialog, 10, uintptr(unsafe.Pointer(&flags))) // GetOptions
	if failedHRESULT(hr) {
		return nil, fmt.Errorf("GetOptions: 0x%08X", uint32(hr))
	}
	flags |= fosForceFileSystem | fosPathMustExist
	if folders {
		flags |= fosPickFolders
	} else {
		flags |= fosAllowMultiSelect | fosFileMustExist
	}
	hr = invokeCOM(dialog, 9, uintptr(flags)) // SetOptions
	if failedHRESULT(hr) {
		return nil, fmt.Errorf("SetOptions: 0x%08X", uint32(hr))
	}

	title := wstr("入力する音声・動画ファイルを選択（複数選択可）")
	if folders {
		title = wstr("フォルダーを選択")
	}
	hr = invokeCOM(dialog, 17, uintptr(unsafe.Pointer(title))) // SetTitle
	runtime.KeepAlive(title)
	if failedHRESULT(hr) {
		return nil, fmt.Errorf("SetTitle: 0x%08X", uint32(hr))
	}

	if !folders {
		label1 := wstr("音声・動画ファイル")
		pattern1 := wstr("*.wav;*.wave;*.mp3;*.m4a;*.aac;*.flac;*.ogg;*.oga;*.opus;*.wma;*.aiff;*.aif;*.aifc;*.alac;*.ape;*.wv;*.caf;*.ac3;*.mka;*.amr;*.mp4;*.mov;*.mkv;*.webm;*.3gp;*.3g2;*.mp2;*.m4b;*.ts;*.mts;*.m2ts;*.pcm")
		label2 := wstr("すべてのファイル")
		pattern2 := wstr("*.*")
		specs := []filterSpec{{label1, pattern1}, {label2, pattern2}}
		hr = invokeCOM(dialog, 4, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0]))) // SetFileTypes
		runtime.KeepAlive(label1)
		runtime.KeepAlive(pattern1)
		runtime.KeepAlive(label2)
		runtime.KeepAlive(pattern2)
		runtime.KeepAlive(specs)
		if failedHRESULT(hr) {
			return nil, fmt.Errorf("SetFileTypes: 0x%08X", uint32(hr))
		}
	}

	hr = invokeCOM(dialog, 3, a.hwnd) // Show, modal
	if uint32(hr) == hresultCancelled {
		return nil, nil
	}
	if failedHRESULT(hr) {
		return nil, fmt.Errorf("Show: 0x%08X", uint32(hr))
	}

	if folders {
		var item uintptr
		hr = invokeCOM(dialog, 20, uintptr(unsafe.Pointer(&item))) // GetResult
		if failedHRESULT(hr) || item == 0 {
			return nil, fmt.Errorf("GetResult: 0x%08X", uint32(hr))
		}
		defer invokeCOM(item, 2)
		path, err := shellItemPath(item)
		if err != nil {
			return nil, err
		}
		return []string{path}, nil
	}

	var arr uintptr
	hr = invokeCOM(dialog, 27, uintptr(unsafe.Pointer(&arr))) // IFileOpenDialog.GetResults
	if failedHRESULT(hr) || arr == 0 {
		return nil, fmt.Errorf("GetResults: 0x%08X", uint32(hr))
	}
	defer invokeCOM(arr, 2)
	var count uint32
	hr = invokeCOM(arr, 7, uintptr(unsafe.Pointer(&count))) // IShellItemArray.GetCount
	if failedHRESULT(hr) {
		return nil, fmt.Errorf("GetCount: 0x%08X", uint32(hr))
	}
	paths := make([]string, 0, count)
	for i := uint32(0); i < count; i++ {
		var item uintptr
		hr = invokeCOM(arr, 8, uintptr(i), uintptr(unsafe.Pointer(&item))) // GetItemAt
		if failedHRESULT(hr) || item == 0 {
			continue
		}
		path, err := shellItemPath(item)
		invokeCOM(item, 2)
		if err == nil && path != "" {
			paths = append(paths, path)
		}
	}
	return paths, nil
}
