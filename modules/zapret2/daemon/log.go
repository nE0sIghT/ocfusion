// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"sync"
)

type boundedLog struct {
	mu   sync.Mutex
	path string
}

func (log *boundedLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	original := len(data)
	const limit = 65536
	if len(data) > limit {
		data = data[len(data)-limit:]
	}

	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(log.path); err == nil && info.Size()+int64(len(data)) > limit {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}

	file, err := os.OpenFile(log.path, flags, 0600)
	if err != nil {
		return 0, err
	}

	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return 0, err
	}

	return original, closeErr
}
