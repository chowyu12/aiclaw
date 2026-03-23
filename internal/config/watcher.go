package config

import (
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

var savingYAML atomic.Bool

// MarkSavingYAML 在即将写回 config.yaml 前后调用，避免监听到自身写入触发无意义重载。
func MarkSavingYAML() {
	savingYAML.Store(true)
	time.AfterFunc(400*time.Millisecond, func() { savingYAML.Store(false) })
}

// StartConfigWatcher 监听配置文件变更并 debounce 后回调（用于热加载）。
// path 建议使用 filepath.Abs 后的绝对路径。
func StartConfigWatcher(path string, onReload func() error) (*fsnotify.Watcher, error) {
	want, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(want)
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, err
	}

	var debounce *time.Timer
	debounceCh := make(chan struct{}, 1)

	go func() {
		for {
			select {
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				got, err := filepath.Abs(filepath.Clean(ev.Name))
				if err != nil || got != want {
					continue
				}
				if !eventIsWrite(ev) {
					continue
				}
				if savingYAML.Load() {
					continue
				}
				if debounce != nil {
					debounce.Stop()
				}
				debounce = time.AfterFunc(300*time.Millisecond, func() {
					select {
					case debounceCh <- struct{}{}:
					default:
					}
				})
			case err, ok := <-w.Errors:
				if !ok {
					return
				}
				if err != nil {
					log.WithError(err).Warn("config watcher error")
				}
			}
		}
	}()

	go func() {
		for range debounceCh {
			if savingYAML.Load() {
				continue
			}
			if err := onReload(); err != nil {
				log.WithError(err).Warn("config hot reload failed")
				continue
			}
			log.WithField("path", want).Info("config reloaded from disk")
		}
	}()

	return w, nil
}

func eventIsWrite(ev fsnotify.Event) bool {
	return ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) != 0
}
