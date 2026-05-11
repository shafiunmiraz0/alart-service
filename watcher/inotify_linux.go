package watcher

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// inotify event flags
const (
	IN_CREATE      = 0x00000100
	IN_DELETE      = 0x00000200
	IN_MODIFY      = 0x00000002
	IN_MOVED_FROM  = 0x00000040
	IN_MOVED_TO    = 0x00000080
	IN_ATTRIB      = 0x00000004
	IN_CLOSE_WRITE = 0x00000008
	IN_OPEN        = 0x00000020
	IN_ACCESS      = 0x00000001
	IN_ISDIR       = 0x40000000
)

// watchWithInotify uses raw inotify syscalls to monitor /etc.
func (w *EtcWatcher) watchWithInotify(paths []string) error {
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC)
	if err != nil {
		return fmt.Errorf("inotify_init1: %w", err)
	}
	defer syscall.Close(fd)

	// Map watch descriptors back to paths.
	wdPaths := make(map[int32]string)

	mask := uint32(IN_CREATE | IN_DELETE | IN_MODIFY | IN_MOVED_FROM |
		IN_MOVED_TO | IN_ATTRIB | IN_CLOSE_WRITE | IN_OPEN | IN_ACCESS)

	for _, root := range paths {
		if err := w.addWatches(fd, root, mask, wdPaths); err != nil {
			log.Printf("[etc-watcher] warning: failed to watch %s: %v", root, err)
		}
	}

	log.Printf("[etc-watcher] watching %d paths under /etc", len(wdPaths))

	// Read events in a loop.
	buf := make([]byte, 4096)
	for {
		select {
		case <-w.stopCh:
			log.Println("[etc-watcher] stopping")
			return nil
		default:
		}

		n, err := syscall.Read(fd, buf)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return fmt.Errorf("read inotify: %w", err)
		}

		if n < syscall.SizeofInotifyEvent {
			continue
		}

		w.processEvents(buf[:n], wdPaths, fd, mask)
	}
}

// addWatches recursively adds inotify watches.
func (w *EtcWatcher) addWatches(fd int, root string, mask uint32, wdPaths map[int32]string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip inaccessible paths.
		}
		if !info.IsDir() {
			return nil
		}
		// Skip certain virtual/problematic directories.
		base := filepath.Base(path)
		if base == ".git" || base == "alternatives" {
			return filepath.SkipDir
		}

		wd, err := addInotifyWatch(fd, path, mask)
		if err != nil {
			log.Printf("[etc-watcher] skip %s: %v", path, err)
			return nil
		}
		wdPaths[int32(wd)] = path

		if !w.cfg.Recursive && path != root {
			return filepath.SkipDir
		}
		return nil
	})
}

// addInotifyWatch adds a single inotify watch using syscall.
func addInotifyWatch(fd int, path string, mask uint32) (int, error) {
	pathBytes, err := syscall.BytePtrFromString(path)
	if err != nil {
		return 0, err
	}
	wd, _, errno := syscall.Syscall(
		syscall.SYS_INOTIFY_ADD_WATCH,
		uintptr(fd),
		uintptr(unsafe.Pointer(pathBytes)),
		uintptr(mask),
	)
	if errno != 0 {
		return 0, errno
	}
	return int(wd), nil
}

// processEvents parses raw inotify events from the buffer.
func (w *EtcWatcher) processEvents(buf []byte, wdPaths map[int32]string, fd int, mask uint32) {
	offset := 0
	for offset < len(buf) {
		if offset+syscall.SizeofInotifyEvent > len(buf) {
			break
		}

		raw := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[offset]))
		nameLen := int(raw.Len)

		var name string
		if nameLen > 0 {
			nameStart := offset + syscall.SizeofInotifyEvent
			nameEnd := nameStart + nameLen
			if nameEnd > len(buf) {
				break
			}
			nameBytes := buf[nameStart:nameEnd]
			// Trim null bytes.
			name = strings.TrimRight(string(nameBytes), "\x00")
		}

		offset += syscall.SizeofInotifyEvent + nameLen

		dir, ok := wdPaths[raw.Wd]
		if !ok {
			continue
		}

		fullPath := filepath.Join(dir, name)

		eventType := describeEvent(raw.Mask)
		if eventType == "" {
			continue
		}

		log.Printf("[etc-watcher] %s: %s", eventType, fullPath)
		w.notifyFileEvent(eventType, fullPath)

		// If a new directory was created, add a watch for it too.
		if raw.Mask&IN_CREATE != 0 && raw.Mask&IN_ISDIR != 0 {
			if w.cfg.Recursive {
				wd, err := addInotifyWatch(fd, fullPath, mask)
				if err == nil {
					wdPaths[int32(wd)] = fullPath
				}
			}
		}
	}
}

// describeEvent returns a human-readable event description.
func describeEvent(mask uint32) string {
	var events []string

	if mask&IN_CREATE != 0 {
		events = append(events, "CREATED")
	}
	if mask&IN_DELETE != 0 {
		events = append(events, "DELETED")
	}
	if mask&IN_MODIFY != 0 {
		events = append(events, "MODIFIED")
	}
	if mask&IN_MOVED_FROM != 0 {
		events = append(events, "MOVED_FROM")
	}
	if mask&IN_MOVED_TO != 0 {
		events = append(events, "MOVED_TO")
	}
	if mask&IN_ATTRIB != 0 {
		events = append(events, "ATTRIB_CHANGED")
	}
	if mask&IN_CLOSE_WRITE != 0 {
		events = append(events, "CLOSE_WRITE")
	}
	// Skip pure OPEN/ACCESS events to reduce noise — only alert on modifications.
	if len(events) == 0 {
		return ""
	}

	return strings.Join(events, "|")
}
