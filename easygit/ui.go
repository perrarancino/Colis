package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"easygit/internal/icon"
)

// Control IDs.
const (
	idFolder = 101 + iota
	idBrowse
	idRepo
	idToken
	idGetToken
	idSignIn
	idSignOut
	idMessage
	idSync
	idStatus
	idAuto
	idAutostart
	idTray
	idLog
	idAccount
)

const (
	wmRefresh  = wmApp + 2 // status/log changed (posted from workers)
	wmFSChange = wmApp + 3 // watcher saw a change
	wmTray     = wmApp + 1
	wmAccount  = wmApp + 4 // sign-in finished
)

const (
	timerDebounce = 1
	timerPoll     = 2
	debounceMs    = 4000
	pollMs        = 45000
)

const (
	kindIdle = iota
	kindBusy
	kindOK
	kindError
)

type App struct {
	hwnd  uintptr
	built bool // controls exist; before that, EN_CHANGE must not overwrite settings
	cfg   Config
	token string
	exe   string

	mu      sync.Mutex
	status  string
	kind    int
	logs    []string
	syncing bool
	dirty   bool // files changed while a sync was running
	etag    string

	watcher *Watcher
	dpi     int

	bigIcon, smallIcon uintptr
	brushWhite         uintptr
	brushGray          uintptr
	fonts              struct{ normal, small, title, bold, mono uintptr }
	ctl                map[int]uintptr
	labels             map[uintptr]bool // static controls drawn in the muted colour
}

var app = &App{ctl: map[int]uintptr{}, labels: map[uintptr]bool{}}

var (
	colText   = rgb(0x1f, 0x23, 0x28)
	colMuted  = rgb(0x6e, 0x77, 0x81)
	colGreen  = rgb(0x1a, 0x7f, 0x37)
	colRed    = rgb(0xcf, 0x22, 0x2e)
	colBlue   = rgb(0x09, 0x69, 0xda)
	colAccent = rgb(0x2d, 0xa4, 0x4e)
)

func (a *App) sc(v int) uintptr { return uintptr(v * a.dpi / 96) }

func runGUI() {
	exe, _ := os.Executable()
	app.exe = exe

	_, _, lastErr := pCreateMutex.Call(0, 1, uintptr(unsafe.Pointer(u16("Local\\EasyGitSingleton"))))
	if lastErr == syscall.Errno(183) { // ERROR_ALREADY_EXISTS
		if h := call(pFindWindow, uintptr(unsafe.Pointer(u16("EasyGitWnd"))), 0); h != 0 {
			postMsg(h, wmTray, 0, wmLButtonUp) // ask the running copy to show itself
		}
		return
	}

	minimized := false
	for _, s := range os.Args[1:] {
		if s == "--minimized" {
			minimized = true
		}
	}

	app.cfg = LoadConfig()
	app.token = LoadToken()
	app.dpi = 96
	if d := int(call(pGetDpiForSystem)); d > 0 {
		app.dpi = d
	}
	app.run(minimized)
}

func (a *App) run(minimized bool) {
	inst := call(pGetModuleHandle, 0)
	a.bigIcon = makeIcon(icon.BGRAPremultiplied(32), 32)
	a.smallIcon = makeIcon(icon.BGRAPremultiplied(16), 16)
	a.brushWhite = call(pCreateSolidBrush, rgb(255, 255, 255))
	a.brushGray = call(pCreateSolidBrush, rgb(0xf6, 0xf8, 0xfa))

	cls := u16("EasyGitWnd")
	wc := wndClassEx{
		Size:       uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:    syscall.NewCallback(wndProc),
		Instance:   inst,
		Icon:       a.bigIcon,
		IconSm:     a.smallIcon,
		Cursor:     call(pLoadCursor, 0, idcArrow),
		Background: a.brushWhite,
		ClassName:  cls,
	}
	call(pRegisterClassEx, uintptr(unsafe.Pointer(&wc)))

	style := uint32(wsCaption | wsSysmenu | wsMinimizebox | wsClipChildren)
	cw, ch := 400, 566
	r := rect{0, 0, int32(a.sc(cw)), int32(a.sc(ch))}
	call(pAdjustWindowRectDpi, uintptr(unsafe.Pointer(&r)), uintptr(style), 0, wsExControlPar, uintptr(a.dpi))
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	x := (int(call(pGetSystemMetrics, 0)) - w) / 2
	y := (int(call(pGetSystemMetrics, 1)) - h) / 2

	a.hwnd = call(pCreateWindowEx, wsExControlPar, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("Easy Git"))),
		uintptr(style), uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, inst, 0)

	if !minimized || a.cfg.Folder == "" {
		call(pShowWindow, a.hwnd, swShow)
		call(pUpdateWindow, a.hwnd)
	}

	a.trayAdd()
	a.restartWatcher()
	call(pSetTimer, a.hwnd, timerPoll, pollMs, 0)
	if a.ready() == "" && a.cfg.AutoSync {
		a.startSync() // catch up with GitHub on launch
	} else {
		a.setStatus(kindIdle, a.idleText())
	}

	var m msg
	for call(pGetMessage, uintptr(unsafe.Pointer(&m)), 0, 0, 0) != 0 {
		if call(pIsDialogMessage, a.hwnd, uintptr(unsafe.Pointer(&m))) != 0 {
			continue
		}
		call(pTranslateMessage, uintptr(unsafe.Pointer(&m)))
		call(pDispatchMessage, uintptr(unsafe.Pointer(&m)))
	}
}

// ---- building the window ---------------------------------------------------------

func (a *App) font(face string, pt int, weight int) uintptr {
	height := -(pt * a.dpi / 72)
	return call(pCreateFont, uintptr(height), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16(face))))
}

func (a *App) mk(class, text string, style, ex uint32, x, y, w, h int, id int, font uintptr) uintptr {
	hw := call(pCreateWindowEx, uintptr(ex), uintptr(unsafe.Pointer(u16(class))), uintptr(unsafe.Pointer(u16(text))),
		uintptr(style|wsChild|wsVisible), a.sc(x), a.sc(y), a.sc(w), a.sc(h), a.hwnd, uintptr(id), 0, 0)
	if font == 0 {
		font = a.fonts.normal
	}
	sendMsg(hw, wmSetFont, font, 1)
	a.ctl[id] = hw
	return hw
}

func (a *App) label(text string, y int) {
	hw := a.mk("STATIC", text, 0, 0, 24, y, 352, 16, 0, a.fonts.small)
	a.labels[hw] = true
}

func (a *App) build() {
	a.fonts.normal = a.font("Segoe UI", 9, 400)
	a.fonts.small = a.font("Segoe UI", 8, 600)
	a.fonts.title = a.font("Segoe UI Semibold", 17, 600)
	a.fonts.bold = a.font("Segoe UI Semibold", 10, 600)
	a.fonts.mono = a.font("Consolas", 8, 400)

	a.mk("STATIC", "Easy Git", 0, 0, 24, 16, 200, 34, 0, a.fonts.title)

	a.label("FOLDER", 62)
	a.mk("EDIT", a.cfg.Folder, wsTabstop|esAutoHScroll|esReadonly, wsExClientEdge, 24, 80, 262, 26, idFolder, 0)
	a.mk("BUTTON", "Browse…", wsTabstop, 0, 294, 79, 82, 28, idBrowse, 0)

	a.label("GITHUB REPOSITORY", 122)
	repo := a.mk("EDIT", a.cfg.Repo, wsTabstop|esAutoHScroll, wsExClientEdge, 24, 140, 352, 26, idRepo, 0)
	sendMsg(repo, emCueBanner, 1, uintptr(unsafe.Pointer(u16("owner/name or https://github.com/owner/name"))))

	a.label("ACCOUNT", 182)
	tok := a.mk("EDIT", "", wsTabstop|esAutoHScroll|esPassword, wsExClientEdge, 24, 200, 178, 26, idToken, 0)
	sendMsg(tok, emCueBanner, 1, uintptr(unsafe.Pointer(u16("paste GitHub token"))))
	a.mk("BUTTON", "Get token", wsTabstop, 0, 210, 199, 82, 28, idGetToken, 0)
	a.mk("BUTTON", "Sign in", wsTabstop, 0, 300, 199, 76, 28, idSignIn, 0)
	a.mk("STATIC", "", 0, 0, 24, 205, 260, 20, idAccount, a.fonts.bold)
	a.mk("BUTTON", "Sign out", wsTabstop, 0, 294, 199, 82, 28, idSignOut, 0)

	a.label("COMMIT MESSAGE (OPTIONAL)", 242)
	msgEdit := a.mk("EDIT", a.cfg.Message, wsTabstop|esAutoHScroll, wsExClientEdge, 24, 260, 352, 26, idMessage, 0)
	sendMsg(msgEdit, emCueBanner, 1, uintptr(unsafe.Pointer(u16("automatic if empty"))))

	a.mk("BUTTON", "Commit & Sync", wsTabstop|bsOwnerDraw, 0, 24, 304, 352, 44, idSync, a.fonts.bold)
	a.mk("STATIC", "", ssEndEllipsis, 0, 24, 360, 352, 20, idStatus, a.fonts.bold)

	a.checkbox(idAuto, "Auto-sync: commit on every change, pull from GitHub", 392, a.cfg.AutoSync)
	a.checkbox(idAutostart, "Start with Windows", 416, a.cfg.Autostart)
	a.checkbox(idTray, "Keep running in the tray when closed", 440, a.cfg.Tray)

	a.mk("EDIT", "", wsTabstop|esMultiline|esReadonly|wsVScroll|esAutoVScroll, 0, 24, 476, 352, 70, idLog, a.fonts.mono)
	a.applyAccount()
	a.built = true
}

func (a *App) checkbox(id int, text string, y int, on bool) {
	hw := a.mk("BUTTON", text, wsTabstop|bsAutoCheck, 0, 24, y, 352, 22, id, 0)
	if on {
		sendMsg(hw, bmSetCheck, 1, 0)
	}
}

// Show the token box when signed out, the account name when signed in.
func (a *App) applyAccount() {
	in := a.token != ""
	show := func(id int, v bool) {
		f := uintptr(swHide)
		if v {
			f = swShow
		}
		call(pShowWindow, a.ctl[id], f)
	}
	show(idToken, !in)
	show(idGetToken, !in)
	show(idSignIn, !in)
	show(idAccount, in)
	show(idSignOut, in)
	if in {
		who := a.cfg.User
		if who == "" {
			who = "GitHub"
		}
		setText(a.ctl[idAccount], "●  Signed in as "+who)
	}
}

// ---- window procedure ------------------------------------------------------------

func wndProc(hwnd, m uintptr, wp, lp uintptr) uintptr {
	a := app
	switch uint32(m) {
	case wmCreate:
		a.hwnd = hwnd
		a.build()
		return 0

	case wmCommand:
		a.onCommand(loword(wp), hiword(wp), lp)
		return 0

	case wmCtlColorStatic, wmCtlColorEdit, wmCtlColorBtn:
		hdc, ctl := wp, lp
		call(pSetBkMode, hdc, transparent)
		switch {
		case ctl == a.ctl[idFolder] || ctl == a.ctl[idLog]:
			call(pSetBkColor, hdc, rgb(0xf6, 0xf8, 0xfa))
			call(pSetTextColor, hdc, colText)
			if ctl == a.ctl[idLog] {
				call(pSetTextColor, hdc, colMuted)
			}
			return a.brushGray
		case ctl == a.ctl[idStatus]:
			a.mu.Lock()
			k := a.kind
			a.mu.Unlock()
			col := colMuted
			switch k {
			case kindOK:
				col = colGreen
			case kindError:
				col = colRed
			case kindBusy:
				col = colBlue
			}
			call(pSetTextColor, hdc, col)
		case ctl == a.ctl[idAccount]:
			call(pSetTextColor, hdc, colGreen)
		case a.labels[ctl]:
			call(pSetTextColor, hdc, colMuted)
		default:
			call(pSetTextColor, hdc, colText)
		}
		if uint32(m) == wmCtlColorEdit {
			call(pSetBkMode, hdc, 2) // opaque
			call(pSetBkColor, hdc, rgb(255, 255, 255))
		}
		return a.brushWhite

	case wmDrawItem:
		di := (*drawItem)(unsafe.Pointer(lp))
		if di.CtlID == idSync {
			a.drawSyncButton(di)
			return 1
		}

	case wmTimer:
		switch wp {
		case timerDebounce:
			call(pKillTimer, hwnd, timerDebounce)
			a.startSync()
		case timerPoll:
			go a.pollRemote()
		}
		return 0

	case wmRefresh:
		a.refresh()
		return 0

	case wmFSChange:
		a.mu.Lock()
		busy := a.syncing
		if busy {
			a.dirty = true
		}
		a.mu.Unlock()
		if !busy && a.cfg.AutoSync {
			call(pSetTimer, hwnd, timerDebounce, debounceMs, 0) // restarts the countdown
		}
		return 0

	case wmAccount:
		a.applyAccount()
		a.refresh()
		return 0

	case wmTray:
		switch uint32(lp) {
		case wmLButtonUp, wmLButtonDblClk:
			call(pShowWindow, hwnd, swRestore)
			call(pSetForegroundWindow, hwnd)
		case wmRButtonUp:
			a.trayMenu()
		}
		return 0

	case wmClose:
		if a.cfg.Tray {
			call(pShowWindow, hwnd, swHide)
			return 0
		}
		call(pDestroyWindow, hwnd)
		return 0

	case wmDestroy:
		a.trayRemove()
		if a.watcher != nil {
			a.watcher.Stop()
		}
		call(pPostQuitMessage, 0)
		return 0
	}
	return call(pDefWindowProc, hwnd, m, wp, lp)
}

func (a *App) onCommand(id, code uint32, lp uintptr) {
	switch id {
	case idBrowse:
		if dir := pickFolder(a.hwnd, "Choose the project folder"); dir != "" {
			a.cfg.Folder = dir
			a.saveCfg()
			setText(a.ctl[idFolder], dir)
			a.restartWatcher()
			a.setStatus(kindIdle, a.idleText())
		}

	case idRepo:
		if code == enChange && a.built {
			a.cfg.Repo = strings.TrimSpace(getText(a.ctl[idRepo]))
			a.cfg.Branch = ""
			a.saveCfg()
			a.mu.Lock()
			a.etag = ""
			a.mu.Unlock()
		}

	case idMessage:
		if code == enChange && a.built {
			a.cfg.Message = getText(a.ctl[idMessage])
			a.saveCfg()
		}

	case idGetToken:
		openURL("https://github.com/settings/tokens/new?scopes=repo&description=EasyGit")

	case idSignIn:
		tok := strings.TrimSpace(getText(a.ctl[idToken]))
		if tok == "" {
			showMessage(a.hwnd, "Click \"Get token\", create a token with the repo scope, and paste it here.")
			return
		}
		a.setStatus(kindBusy, "Signing in…")
		go func() {
			login, err := NewGitHub(tok, "", "").User()
			if err != nil {
				a.logf("Sign-in failed: " + err.Error())
				a.setStatus(kindError, "Sign-in failed — check the token")
				return
			}
			if err := SaveToken(tok); err != nil {
				a.logf("Could not store the token: " + err.Error())
			}
			a.token = tok
			a.cfg.User = login
			a.saveCfg()
			a.logf("Signed in as " + login)
			a.setStatus(kindIdle, a.idleText())
			sendMsg(a.ctl[idToken], 0x000C, 0, uintptr(unsafe.Pointer(u16("")))) // WM_SETTEXT: clear the box
			postMsg(a.hwnd, wmAccount, 0, 0)
		}()

	case idSignOut:
		_ = SaveToken("")
		a.token = ""
		a.cfg.User = ""
		a.saveCfg()
		a.applyAccount()
		a.setStatus(kindIdle, a.idleText())
		a.logf("Signed out")

	case idSync:
		a.startSync()

	case idAuto:
		a.cfg.AutoSync = sendMsg(a.ctl[idAuto], bmGetCheck, 0, 0) == 1
		a.saveCfg()
		a.restartWatcher()
		if a.cfg.AutoSync && a.ready() == "" {
			a.startSync()
		} else {
			a.setStatus(kindIdle, a.idleText())
		}

	case idAutostart:
		a.cfg.Autostart = sendMsg(a.ctl[idAutostart], bmGetCheck, 0, 0) == 1
		a.saveCfg()
		if err := SetAutostart(a.exe, a.cfg.Autostart); err != nil {
			a.logf("Autostart: " + err.Error())
		}

	case idTray:
		a.cfg.Tray = sendMsg(a.ctl[idTray], bmGetCheck, 0, 0) == 1
		a.saveCfg()

	case 1001: // tray: open
		call(pShowWindow, a.hwnd, swRestore)
		call(pSetForegroundWindow, a.hwnd)
	case 1002: // tray: sync
		a.startSync()
	case 1003: // tray: exit
		call(pDestroyWindow, a.hwnd)
	}
}

func (a *App) saveCfg() {
	if err := a.cfg.Save(); err != nil {
		a.logf("Could not save settings: " + err.Error())
	}
}

// ---- drawing ---------------------------------------------------------------------

func (a *App) drawSyncButton(di *drawItem) {
	fill := colAccent
	switch {
	case di.ItemState&odsDisabled != 0:
		fill = rgb(0x94, 0xd3, 0xa2)
	case di.ItemState&odsSelected != 0:
		fill = rgb(0x1f, 0x88, 0x3d)
	}
	call(pFillRect, di.HDC, uintptr(unsafe.Pointer(&di.RcItem)), a.brushWhite)
	br := call(pCreateSolidBrush, fill)
	oldBr := call(pSelectObject, di.HDC, br)
	oldPen := call(pSelectObject, di.HDC, call(pGetStockObject, nullPen))
	rad := a.sc(10)
	call(pRoundRect, di.HDC, uintptr(di.RcItem.Left), uintptr(di.RcItem.Top), uintptr(di.RcItem.Right+1), uintptr(di.RcItem.Bottom+1), rad, rad)
	call(pSelectObject, di.HDC, oldPen)
	call(pSelectObject, di.HDC, oldBr)
	call(pDeleteObject, br)

	call(pSetBkMode, di.HDC, transparent)
	call(pSetTextColor, di.HDC, rgb(255, 255, 255))
	old := call(pSelectObject, di.HDC, a.fonts.bold)
	text := getText(di.HwndItem)
	buf := utf16(text)
	call(pDrawText, di.HDC, uintptr(unsafe.Pointer(&buf[0])), ^uintptr(0), uintptr(unsafe.Pointer(&di.RcItem)), dtCenter|dtVCenter|dtSingleLine|dtNoPrefix)
	call(pSelectObject, di.HDC, old)
}

func utf16(s string) []uint16 {
	b, _ := syscall.UTF16FromString(s)
	return b
}

// ---- status, log -----------------------------------------------------------------

func (a *App) setStatus(kind int, text string) {
	a.mu.Lock()
	a.kind, a.status = kind, text
	a.mu.Unlock()
	if a.hwnd != 0 {
		postMsg(a.hwnd, wmRefresh, 0, 0)
	}
}

func (a *App) logf(s string) {
	a.mu.Lock()
	a.logs = append(a.logs, time.Now().Format("15:04:05")+"  "+s)
	if len(a.logs) > 200 {
		a.logs = a.logs[len(a.logs)-200:]
	}
	a.mu.Unlock()
	if a.hwnd != 0 {
		postMsg(a.hwnd, wmRefresh, 0, 0)
	}
}

func (a *App) refresh() {
	a.mu.Lock()
	status, kind, busy := a.status, a.kind, a.syncing
	logText := strings.Join(a.logs, "\r\n")
	a.mu.Unlock()

	prefix := "●  "
	setText(a.ctl[idStatus], prefix+status)
	call(pInvalidateRect, a.ctl[idStatus], 0, 1)
	if getText(a.ctl[idLog]) != logText {
		setText(a.ctl[idLog], logText)
		n := uintptr(len(logText))
		sendMsg(a.ctl[idLog], emSetSel, n, n)
		sendMsg(a.ctl[idLog], emScrollCrt, 0, 0)
	}

	label := "Commit & Sync"
	if busy {
		label = "Syncing…"
	}
	if getText(a.ctl[idSync]) != label {
		setText(a.ctl[idSync], label)
	}
	enable := uintptr(1)
	if busy {
		enable = 0
	}
	call(pEnableWindow, a.ctl[idSync], enable)
	a.trayTip(status)
	_ = kind
}

func (a *App) idleText() string {
	if why := a.ready(); why != "" {
		return why
	}
	if a.cfg.AutoSync {
		return "Auto-sync is on"
	}
	return "Ready — press Commit & Sync"
}

// ready returns what is still missing before a sync can run ("" when ready).
func (a *App) ready() string {
	switch {
	case a.cfg.Folder == "":
		return "Choose a project folder"
	case func() bool { _, _, ok := SplitRepo(a.cfg.Repo); return !ok }():
		return "Enter the GitHub repository"
	case a.token == "":
		return "Sign in to GitHub"
	}
	return ""
}

// ---- syncing ---------------------------------------------------------------------

func (a *App) startSync() {
	if why := a.ready(); why != "" {
		a.setStatus(kindError, why)
		return
	}
	a.mu.Lock()
	if a.syncing {
		a.mu.Unlock()
		return
	}
	a.syncing = true
	a.dirty = false
	a.mu.Unlock()
	a.setStatus(kindBusy, "Syncing…")

	owner, name, _ := SplitRepo(a.cfg.Repo)
	folder, branch, message, token := a.cfg.Folder, a.cfg.Branch, a.cfg.Message, a.token
	go func() {
		e := NewEngine(folder, owner, name, branch, token, a.logf)
		res, err := e.Sync(message)

		a.mu.Lock()
		a.syncing = false
		again := a.dirty
		a.etag = "" // our own push moved the branch; start polling from here
		a.mu.Unlock()

		if err != nil {
			a.logf("Error: " + err.Error())
			a.setStatus(kindError, "Sync failed — "+shorten(err.Error(), 60))
		} else {
			if a.cfg.Branch == "" && e.Branch != "" {
				a.cfg.Branch = e.Branch
				a.saveCfg()
			}
			a.logf(res.String())
			a.setStatus(kindOK, fmt.Sprintf("Synced at %s — %s", time.Now().Format("15:04"), res.String()))
		}
		if again && a.cfg.AutoSync {
			postMsg(a.hwnd, wmFSChange, 0, 0)
		}
		postMsg(a.hwnd, wmRefresh, 0, 0)
	}()
}

// pollRemote checks the branch with a conditional request (free when unchanged)
// and triggers a sync when GitHub has something new.
func (a *App) pollRemote() {
	if !a.cfg.AutoSync || a.ready() != "" {
		return
	}
	a.mu.Lock()
	busy, etag := a.syncing, a.etag
	a.mu.Unlock()
	if busy || a.cfg.Branch == "" {
		return
	}
	owner, name, _ := SplitRepo(a.cfg.Repo)
	changed, newTag, err := NewGitHub(a.token, owner, name).PollRef(a.cfg.Branch, etag)
	if err != nil {
		return // offline etc.: try again next tick
	}
	a.mu.Lock()
	a.etag = newTag
	a.mu.Unlock()
	if changed && etag != "" {
		a.logf("GitHub has new commits")
		postMsg(a.hwnd, wmFSChange, 0, 0)
	}
}

func shorten(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return string(r)
}

func (a *App) restartWatcher() {
	if a.watcher != nil {
		a.watcher.Stop()
		a.watcher = nil
	}
	if !a.cfg.AutoSync || a.cfg.Folder == "" {
		return
	}
	if st, err := os.Stat(a.cfg.Folder); err != nil || !st.IsDir() {
		return
	}
	w, err := StartWatcher(filepath.Clean(a.cfg.Folder), func() { postMsg(a.hwnd, wmFSChange, 0, 0) })
	if err != nil {
		a.logf("Cannot watch folder: " + err.Error())
		return
	}
	a.watcher = w
}

// ---- tray ------------------------------------------------------------------------

func (a *App) trayData() notifyIconData {
	return notifyIconData{
		Size: uint32(unsafe.Sizeof(notifyIconData{})), HWnd: a.hwnd, ID: 1,
		Flags: nifMsg | nifIcon | nifTip, CallbackMessage: wmTray, Icon: a.smallIcon,
	}
}

func (a *App) trayAdd() {
	d := a.trayData()
	copy(d.Tip[:], utf16("Easy Git"))
	call(pShellNotifyIcon, nimAdd, uintptr(unsafe.Pointer(&d)))
}

func (a *App) trayTip(text string) {
	d := a.trayData()
	copy(d.Tip[:127], utf16("Easy Git — "+shorten(text, 90)))
	call(pShellNotifyIcon, nimModify, uintptr(unsafe.Pointer(&d)))
}

func (a *App) trayRemove() {
	d := a.trayData()
	call(pShellNotifyIcon, nimDelete, uintptr(unsafe.Pointer(&d)))
}

func (a *App) trayMenu() {
	menu := call(pCreatePopupMenu)
	call(pAppendMenu, menu, mfString, 1001, uintptr(unsafe.Pointer(u16("Open"))))
	call(pAppendMenu, menu, mfString, 1002, uintptr(unsafe.Pointer(u16("Commit && Sync now"))))
	call(pAppendMenu, menu, mfSeparator, 0, 0)
	call(pAppendMenu, menu, mfString, 1003, uintptr(unsafe.Pointer(u16("Exit"))))
	var p point
	call(pGetCursorPos, uintptr(unsafe.Pointer(&p)))
	call(pSetForegroundWindow, a.hwnd)
	cmd := call(pTrackPopupMenu, menu, tpmReturn|tpmRight, uintptr(p.X), uintptr(p.Y), 0, a.hwnd, 0)
	call(pDestroyMenu, menu)
	if cmd != 0 {
		postMsg(a.hwnd, wmCommand, cmd, 0)
	}
}
