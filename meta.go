//  Copyright (c) 2016 Couchbase, Inc.
//  Licensed under the Apache License, Version 2.0 (the "License");
//  you may not use this file except in compliance with the
//  License. You may obtain a copy of the License at
//    http://www.apache.org/licenses/LICENSE-2.0
//  Unless required by applicable law or agreed to in writing,
//  software distributed under the License is distributed on an "AS
//  IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
//  express or implied. See the License for the specific language
//  governing permissions and limitations under the License.

package main

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"
)

// FileMeta represents metadata about a file that needs to be parsed.
type FileMeta struct {
	Skip       bool                   // When true, ignore this FileMeta.
	HeaderSize int                    // The number of lines in a skippable header.
	EntryStart func(line string) bool // Optional, returns true when line starts a new log entry.
	EntryRE    *regexp.Regexp         // Used to parse the first line of a log entry.
	Cleanser   func([]byte) []byte    // Optional, called before tokenizing an entry.
}

// ------------------------------------------------------------

// From memcached.log...
//   2016-04-14T16:10:09.463447-07:00 WARNING Restarting file logging
//
// From ns_server.fts.log...
//   2016-04-14T17:43:52.164-07:00 [INFO] moss_herder: persistence progess, waiting: 3
//
// From ns_server.babysitter.log...
//   [error_logger:info,2016-04-14T16:10:05.262-07:00,babysitter_of_ns_1@127.0.0.1
//
// From ns_server.goxdcr.log...
//   ReplicationManager 2016-04-14T16:10:09.652-07:00 [INFO] GOMAXPROCS=4
//
// From ns_server.http_access.log...
//   172.23.123.146 - Administrator [14/Apr/2016:16:10:19 -0700] \
//     "GET /nodes/self HTTP/1.1" 200 1727 - Python-httplib2/$Rev: 259 $
//
// From query...
//   _time=2016-04-05T13:23:05.378+01:00 _level=INFO _msg=Created New Bucket default
//   2016/04/05 13:23:05  Trying with http://127.0.0.1:8091/pools/default/bucketsStreaming/default
//   2016-04-05T13:24:05.388+01:00 [Info] connected with 1 indexers
//
// From indexer...
//   ==== Index Instance 12648800643524082356 ====
//   2016-04-12T10:35:32.355+01:00 [Info] connected with 1 indexers
//   2016-04-12T10:35:32.355+01:00 [Info] index 17632878461435344554 has 1 replicas
//
// From projector...
//   2016-04-11T20:53:31.327+01:00 [Info] memstats {"Alloc":79226592, ...}
//   2016-04-12T10:17:35.286+01:00 [Info] VBRT[<-49<-travel-sample<-127.0.0.1:8091 \
//     #MAINT_STREAM_TOPIC_bb:44:4a:7f:f5:90:d5:91] ##3b created
//   2016-04-05T13:22:26.133+01:00 [Info] pram[:9999] registered /adminport/vbmapRequest

var ymd = `(?P<year>\d\d\d\d)-(?P<month>\d\d)-(?P<day>\d\d)`
var hms = `T(?P<HH>\d\d):(?P<MM>\d\d):(?P<SS>\d\d)\.(?P<SSSS>\d+)`

var re_ymd_hms = regexp.MustCompile(" " + ymd + hms + " ")

var re_usual = regexp.MustCompile(`^` + ymd + hms + `-\S+\s(?P<level>\S+)\s`)

var re_usual_ex = regexp.MustCompile(`^(?P<module>\w+)\s` + ymd + hms + `-\S+\s(?P<level>\S+)\s`)

var re_ns = regexp.MustCompile(`^\[(?P<module>\w+):(?P<level>\w+),` + ymd + hms + `-[^,]+,`)

// ------------------------------------------------------------

var stringify_replace = []byte(` "$0" `)

var hex = "[a-f0-9]"
var hex6 = hex + hex + hex + hex + hex + hex

var re_uuid = regexp.MustCompile(" " + hex6 + "[a-f0-9-_]+ ")

var re_addr = regexp.MustCompile(`(ns_\d+@)?\d+\.\d+\.\d+.\d+`)

var re_int = regexp.MustCompile(`\d+`)

var equals_bar_re = regexp.MustCompile(`=======+([^=]+)=======+`)
var equals_bar_replace = []byte(`"$1"`)

var ns_pid_re = regexp.MustCompile(`<\d+\.\d+\.\d+>`) // <0.0.0>

// ------------------------------------------------------------

var FileMetaUsual = FileMeta{
	HeaderSize: 4,
	EntryRE:    re_usual,
}

// FileMetaNS represents metadata about an ns-server log file.
var FileMetaNS = FileMeta{
	HeaderSize: 4,
	EntryStart: func(line string) bool {
		if len(line) <= 0 ||
			line[0] != '[' {
			return false
		}
		lineParts := strings.Split(line, ",")
		if len(lineParts) < 3 || len(lineParts[1]) <= 0 {
			return false
		}
		return unicode.IsDigit(rune(lineParts[1][0]))
	},
	EntryRE: re_ns,
	Cleanser: func(s []byte) []byte {
		// Clear out first non-matching ']'.
		rbrack := bytes.Index(s, []byte("]"))
		if rbrack >= 0 {
			lbrack := bytes.Index(s, []byte("["))
			if lbrack < 0 || rbrack < lbrack {
				s[rbrack] = ' '
			}
		}

		// Convert `=============PROGRESS REPORT=============`
		// into `"PROGRESS REPORT"`
		s = equals_bar_re.ReplaceAll(s, equals_bar_replace)

		// Convert `<0.0.0>` into `"<0.0.0>"`
		s = ns_pid_re.ReplaceAll(s, stringify_replace)

		// Convert `ns_1@172.23.105.216` into ` "ns_1@172.23.105.216" `
		s = re_addr.ReplaceAll(s, stringify_replace)

		// Stringify dates.
		s = re_ymd_hms.ReplaceAll(s, stringify_replace)

		// Stringify uuids.
		s = re_uuid.ReplaceAll(s, stringify_replace)

		return s
	},
}

// ------------------------------------------------------------

// FileMetas is keyed by file name.
var FileMetas = map[string]FileMeta{ // Keep alphabetical...
	// TODO: "couchbase.log".

	// TODO: "ddocs.log".

	// TODO: "diag.log".

	// SKIP: "ini.log" -- not a log file.

	// TODO: "master_events.log".

	"memcached.log": {
		HeaderSize: 4,
		EntryRE:    re_usual,
		Cleanser: func(s []byte) []byte {
			s = re_addr.ReplaceAll(s, stringify_replace)
			s = re_uuid.ReplaceAll(s, stringify_replace)
			return s
		},
	},

	"ns_server.babysitter.log": FileMetaNS,

	"ns_server.couchdb.log": FileMetaNS,

	// TODO: "ns_server.debug.log": FileMetaNS, -- too big for now.

	"ns_server.error.log": FileMetaNS,

	"ns_server.fts.log": {
		HeaderSize: 4,
		EntryStart: func(line string) bool {
			return re_usual.MatchString(line)
		},
		EntryRE: re_usual,
		Cleanser: func(s []byte) []byte {
			return bytes.Replace(s, []byte("\n"), []byte(""), -1)
		},
	},

	"ns_server.goxdcr.log": {
		HeaderSize: 4,
		EntryRE:    re_usual_ex,
	},

	"ns_server.http_access.log": {
		Skip:       true,
		HeaderSize: 4,
	},

	"ns_server.http_access_internal.log": {
		Skip:       true,
		HeaderSize: 4,
	},

	"ns_server.indexer.log": FileMetaUsual,

	"ns_server.info.log": FileMetaNS,

	// TODO: "ns_server.mapreduce_errors.log".

	"ns_server.metakv.log": FileMetaNS,

	"ns_server.ns_couchdb.log": FileMetaNS,

	"ns_server.projector.log": FileMetaUsual,

	"ns_server.query.log": FileMetaUsual, // TODO: Revisit.

	"ns_server.reports.log": FileMetaNS,

	"ns_server.ssl_proxy.log": FileMetaNS,

	"ns_server.stats.log": FileMetaNS,

	// TODO: "ns_server.views.log".

	"ns_server.xdcr.log": FileMetaNS,

	// TODO: "ns_server.xdcr_errors.log".

	// TODO: "ns_server.xdcr_trace.log".

	// TODO: "stats.log".

	// TODO: "stats__archives.json".

	// TODO: "syslog.tar.gz".

	// TODO: "systemd_journal.gz".
}
