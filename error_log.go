package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

const maxErrorLogBytes = 1 << 20

var errorSequence atomic.Uint64

type rotatingErrorLog struct {
	path     string
	maxBytes int64
}

func (writer *rotatingErrorLog) Write(entry []byte) (int, error) {
	if info, err := os.Stat(writer.path); err == nil && info.Size() > 0 && info.Size()+int64(len(entry)) > writer.maxBytes {
		if err := os.Remove(writer.path + ".1"); err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		if err := os.Rename(writer.path, writer.path+".1"); err != nil {
			return 0, err
		}
	}
	file, err := os.OpenFile(writer.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	return file.Write(entry)
}

func setupErrorLog() error {
	path := filepath.Join(".runtime", "error.log")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	log.SetOutput(&rotatingErrorLog{path: path, maxBytes: maxErrorLogBytes})
	return nil
}

func nextErrorID() string {
	return fmt.Sprintf("E%x-%x", time.Now().UnixMilli(), errorSequence.Add(1))
}

func logHTTPFailure(id string, status int, method, path string, cause error) {
	message := "unbekannter Fehler"
	if cause != nil {
		message = cause.Error()
	}
	log.Printf("level=ERROR id=%s status=%d method=%s path=%q cause=%q", id, status, method, path, message)
}

func logOperationalFailure(event string, cause error) string {
	id := nextErrorID()
	log.Printf("level=ERROR id=%s event=%s cause=%q", id, event, cause)
	return id
}

func publicServerError(status int) string {
	if status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout {
		return "Ein externer Dienst ist nicht erreichbar. Details stehen im Errorlog."
	}
	return "Interner Serverfehler. Details stehen im Errorlog."
}
