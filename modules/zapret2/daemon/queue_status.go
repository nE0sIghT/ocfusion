// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Counters belong to the current kernel queue and reset when it is reopened.
func queueStatus() any {
	data, err := os.ReadFile(filepath.Join(runDir, "resources.json"))
	if err != nil {
		return nil
	}
	var resources Resources
	if json.Unmarshal(data, &resources) != nil || resources.Queue == 0 {
		return nil
	}
	data, err = readNFQueues()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 || fields[0] != strconv.Itoa(resources.Queue) {
			continue
		}
		values := make([]uint64, 5)
		for i, column := range []int{1, 2, 5, 6, 7} {
			value, err := strconv.ParseUint(fields[column], 10, 64)
			if err != nil {
				return nil
			}
			values[i] = value
		}
		return map[string]any{"id": resources.Queue, "consumer_portid": values[0], "pending": values[1], "kernel_dropped": values[2], "userspace_dropped": values[3], "sequence": values[4]}
	}
	return nil
}
