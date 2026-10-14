package main

import (
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Watcher reports file changes below a folder using ReadDirectoryChangesW,
// so auto-sync costs nothing while nothing happens.
type Watcher struct {
	h    windows.Handle
	done chan struct{}
}

func StartWatcher(dir string, onChange func()) (*Watcher, error) {
	h, err := windows.CreateFile(u16(dir),
		0x1, // FILE_LIST_DIRECTORY
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, err
	}
	w := &Watcher{h: h, done: make(chan struct{})}
	var mu sync.Mutex
	ig := LoadIgnore(dir)

	go func() {
		defer close(w.done)
		defer windows.CloseHandle(h)
		buf := make([]byte, 64*1024)
		const filter = 0x1 | 0x2 | 0x8 | 0x10 | 0x40 // names, size, last write, creation
		for {
			var n uint32
			if err := windows.ReadDirectoryChanges(h, &buf[0], uint32(len(buf)), true, filter, &n, nil, 0); err != nil {
				return // handle cancelled/closed
			}
			if n == 0 { // buffer overflow: something changed, we just don't know what
				onChange()
				continue
			}
			relevant := false
			for off := uint32(0); ; {
				info := (*windows.FileNotifyInformation)(unsafe.Pointer(&buf[off]))
				name := windows.UTF16ToString(unsafe.Slice(&info.FileName, info.FileNameLength/2))
				rel := strings.ReplaceAll(name, "\\", "/")
				if rel == ".gitignore" {
					mu.Lock()
					ig = LoadIgnore(dir)
					mu.Unlock()
					relevant = true
				} else {
					mu.Lock()
					skip := ig.Match(rel, false)
					mu.Unlock()
					if !skip {
						relevant = true
					}
				}
				if info.NextEntryOffset == 0 {
					break
				}
				off += info.NextEntryOffset
			}
			if relevant {
				onChange()
			}
		}
	}()
	return w, nil
}

// Stop cancels the pending read. The cancel is repeated because it is lost if
// the goroutine happens to be between two reads.
func (w *Watcher) Stop() {
	for {
		windows.CancelIoEx(w.h, nil)
		select {
		case <-w.done:
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// ---- start with Windows -----------------------------------------------------------

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func SetAutostart(exe string, on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		err := k.DeleteValue("EasyGit")
		if err == registry.ErrNotExist {
			return nil
		}
		return err
	}
	return k.SetStringValue("EasyGit", `"`+exe+`" --minimized`)
}
