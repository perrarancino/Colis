package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Minimal hand-rolled Win32 bindings: the whole UI is plain windows and
// controls, which keeps the binary small and free of any runtime dependency.

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pRegisterClassEx     = user32.NewProc("RegisterClassExW")
	pCreateWindowEx      = user32.NewProc("CreateWindowExW")
	pDefWindowProc       = user32.NewProc("DefWindowProcW")
	pShowWindow          = user32.NewProc("ShowWindow")
	pUpdateWindow        = user32.NewProc("UpdateWindow")
	pGetMessage          = user32.NewProc("GetMessageW")
	pTranslateMessage    = user32.NewProc("TranslateMessage")
	pDispatchMessage     = user32.NewProc("DispatchMessageW")
	pIsDialogMessage     = user32.NewProc("IsDialogMessageW")
	pPostQuitMessage     = user32.NewProc("PostQuitMessage")
	pPostMessage         = user32.NewProc("PostMessageW")
	pSendMessage         = user32.NewProc("SendMessageW")
	pSetWindowText       = user32.NewProc("SetWindowTextW")
	pGetWindowText       = user32.NewProc("GetWindowTextW")
	pGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	pEnableWindow        = user32.NewProc("EnableWindow")
	pDestroyWindow       = user32.NewProc("DestroyWindow")
	pLoadCursor          = user32.NewProc("LoadCursorW")
	pSetTimer            = user32.NewProc("SetTimer")
	pKillTimer           = user32.NewProc("KillTimer")
	pInvalidateRect      = user32.NewProc("InvalidateRect")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pGetCursorPos        = user32.NewProc("GetCursorPos")
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenu          = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pFillRect            = user32.NewProc("FillRect")
	pDrawText            = user32.NewProc("DrawTextW")
	pGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	pGetDpiForSystem     = user32.NewProc("GetDpiForSystem")
	pAdjustWindowRectDpi = user32.NewProc("AdjustWindowRectExForDpi")
	pCreateIconIndirect  = user32.NewProc("CreateIconIndirect")
	pFindWindow          = user32.NewProc("FindWindowW")
	pMessageBox          = user32.NewProc("MessageBoxW")
	pSetFocus            = user32.NewProc("SetFocus")
	pIsWindowVisible     = user32.NewProc("IsWindowVisible")

	pCreateFont       = gdi32.NewProc("CreateFontW")
	pDeleteObject     = gdi32.NewProc("DeleteObject")
	pCreateSolidBrush = gdi32.NewProc("CreateSolidBrush")
	pSetTextColor     = gdi32.NewProc("SetTextColor")
	pSetBkColor       = gdi32.NewProc("SetBkColor")
	pSetBkMode        = gdi32.NewProc("SetBkMode")
	pSelectObject     = gdi32.NewProc("SelectObject")
	pGetStockObject   = gdi32.NewProc("GetStockObject")
	pRoundRect        = gdi32.NewProc("RoundRect")
	pCreateBitmap     = gdi32.NewProc("CreateBitmap")

	pSHBrowseForFolder   = shell32.NewProc("SHBrowseForFolderW")
	pSHGetPathFromIDList = shell32.NewProc("SHGetPathFromIDListW")
	pShellNotifyIcon     = shell32.NewProc("Shell_NotifyIconW")
	pShellExecute        = shell32.NewProc("ShellExecuteW")

	pCoInitializeEx = ole32.NewProc("CoInitializeEx")
	pCoTaskMemFree  = ole32.NewProc("CoTaskMemFree")

	pGetModuleHandle = kernel32.NewProc("GetModuleHandleW")
	pCreateMutex     = kernel32.NewProc("CreateMutexW")
)

const (
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabstop      = 0x00010000
	wsCaption      = 0x00C00000
	wsSysmenu      = 0x00080000
	wsMinimizebox  = 0x00020000
	wsClipChildren = 0x02000000
	wsExClientEdge = 0x00000200
	wsExControlPar = 0x00010000

	esAutoHScroll = 0x80
	esReadonly    = 0x800
	esMultiline   = 0x4
	esPassword    = 0x20
	esAutoVScroll = 0x40
	esNumber      = 0x2000
	wsVScroll     = 0x00200000
	bsAutoCheck   = 0x3
	bsOwnerDraw   = 0xB
	ssEndEllipsis = 0x4000
	ssNotify      = 0x100

	wmCreate         = 0x0001
	wmDestroy        = 0x0002
	wmClose          = 0x0010
	wmSetFont        = 0x0030
	wmDrawItem       = 0x002B
	wmCommand        = 0x0111
	wmTimer          = 0x0113
	wmCtlColorEdit   = 0x0133
	wmCtlColorBtn    = 0x0135
	wmCtlColorStatic = 0x0138
	wmLButtonUp      = 0x0202
	wmLButtonDblClk  = 0x0203
	wmRButtonUp      = 0x0205
	wmApp            = 0x8000

	bmGetCheck   = 0x00F0
	bmSetCheck   = 0x00F1
	emSetSel     = 0x00B1
	emScrollCrt  = 0x00B7
	emCueBanner  = 0x1501
	enChange     = 0x0300
	enKillFocus  = 0x0200
	swHide       = 0
	swShow       = 5
	swRestore    = 9
	swShowNormal = 1

	colorWindowBrush = 5
	dtCenter         = 0x1
	dtVCenter        = 0x4
	dtSingleLine     = 0x20
	dtNoPrefix       = 0x800
	nullPen          = 8
	transparent      = 1
	odsSelected      = 0x1
	odsDisabled      = 0x4
	idcArrow         = 32512

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2
	nifMsg    = 1
	nifIcon   = 2
	nifTip    = 4

	mfString    = 0
	mfSeparator = 0x800
	tpmReturn   = 0x100
	tpmRight    = 0x2

	biNewDialog  = 0x40
	biReturnDirs = 0x1
)

type wndClassEx struct {
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
	IconSm     uintptr
}

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }

type msg struct {
	HWnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type drawItem struct {
	CtlType, CtlID, ItemID, ItemAction, ItemState uint32
	HwndItem                                      uintptr
	HDC                                           uintptr
	RcItem                                        rect
	ItemData                                      uintptr
}

type iconInfo struct {
	FIcon    uint32
	XHotspot uint32
	YHotspot uint32
	Mask     uintptr
	Color    uintptr
}

type browseInfo struct {
	Owner       uintptr
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Fn          uintptr
	LParam      uintptr
	Image       int32
}

type notifyIconData struct {
	Size            uint32
	HWnd            uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GuidItem        windows.GUID
	BalloonIcon     uintptr
}

func u16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func call(p *windows.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

func rgb(r, g, b uint8) uintptr { return uintptr(uint32(r) | uint32(g)<<8 | uint32(b)<<16) }

func loword(v uintptr) uint32 { return uint32(v & 0xFFFF) }
func hiword(v uintptr) uint32 { return uint32((v >> 16) & 0xFFFF) }

func sendMsg(h uintptr, m uint32, wp, lp uintptr) uintptr {
	return call(pSendMessage, h, uintptr(m), wp, lp)
}

func postMsg(h uintptr, m uint32, wp, lp uintptr) { call(pPostMessage, h, uintptr(m), wp, lp) }

func setText(h uintptr, s string) { call(pSetWindowText, h, uintptr(unsafe.Pointer(u16(s)))) }

func getText(h uintptr) string {
	n := int(call(pGetWindowTextLength, h))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	call(pGetWindowText, h, uintptr(unsafe.Pointer(&buf[0])), uintptr(n+1))
	return syscall.UTF16ToString(buf)
}

func showMessage(owner uintptr, text string) {
	call(pMessageBox, owner, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16("Easy Git"))), 0x40)
}

func openURL(url string) {
	call(pShellExecute, 0, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16(url))), 0, 0, swShowNormal)
}

// makeIcon turns the generated logo into an HICON of the given size.
func makeIcon(bgra []byte, size int) uintptr {
	color := call(pCreateBitmap, uintptr(size), uintptr(size), 1, 32, uintptr(unsafe.Pointer(&bgra[0])))
	mask := call(pCreateBitmap, uintptr(size), uintptr(size), 1, 1, 0)
	ii := iconInfo{FIcon: 1, Mask: mask, Color: color}
	h := call(pCreateIconIndirect, uintptr(unsafe.Pointer(&ii)))
	call(pDeleteObject, color)
	call(pDeleteObject, mask)
	return h
}

// pickFolder shows the standard folder dialog; returns "" when cancelled.
func pickFolder(owner uintptr, title string) string {
	call(pCoInitializeEx, 0, 2) // apartment threaded, needed for the modern dialog
	display := make([]uint16, 260)
	bi := browseInfo{Owner: owner, DisplayName: &display[0], Title: u16(title), Flags: biNewDialog | biReturnDirs}
	pidl := call(pSHBrowseForFolder, uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return ""
	}
	defer call(pCoTaskMemFree, pidl)
	buf := make([]uint16, 520)
	if call(pSHGetPathFromIDList, pidl, uintptr(unsafe.Pointer(&buf[0]))) == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}
