// SPDX-License-Identifier: GPL-3.0-or-later
package main

var version = "dev"
var revision = "unknown"

func versionCommit() string {
	if len(revision) > 12 {
		return revision[:12]
	}

	return revision
}

func main() {
	runCLI()
}
