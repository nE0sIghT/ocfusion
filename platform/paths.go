// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "regexp"

var base = "/jffs/ocfusion-platform"
var ram = "/tmp/ocfusion-platform"
var socket = ram + "/control.sock"

var installMarker = "/tmp/ocfusion-installing"

var packageRoot = "/tmp/opt/ocfusion-modules"

var idRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
var versionRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,47}$`)
