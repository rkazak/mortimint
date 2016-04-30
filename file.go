package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"go/scanner"
	"go/token"
)

type fileProcessor struct {
	run      *Run
	dir      string
	dirBase  string
	fname    string
	fnameOut string
	fmeta    FileMeta
	dict     Dict
	buf      []byte // Reusable buf to reduce garbage.
}

// A tokLit associates a token and a literal string.
type tokLit struct {
	tok     token.Token
	lit     string
	emitted bool // Marked true when this tokLit has been emitted.
}

// ------------------------------------------------------------

func (p *fileProcessor) process() error {
	fmt.Fprintf(os.Stderr, "processing %s/%s\n", p.dirBase, p.fname)

	f, err := os.Open(p.dir + "/" + p.fname)
	if err != nil {
		return err
	}
	defer f.Close()

	// Repeatably scan until we have the consecutive lines to make up
	// an "entry", and invoke processEntry() on every entry.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, ScannerBufferCapacity)

	var currOffset int
	var currLine int

	var entryStartOffset int
	var entryStartLine int
	var entryLines []string

	for scanner.Scan() {
		lineStr := scanner.Text()

		currLine++
		if currLine <= p.fmeta.HeaderSize { // Skip header.
			currOffset += len(lineStr) + 1
			continue
		}

		if p.fmeta.EntryStart == nil || p.fmeta.EntryStart(lineStr) {
			p.processEntry(entryStartOffset, entryStartLine, entryLines)

			entryStartOffset = currOffset
			entryStartLine = currLine
			entryLines = entryLines[0:0]
		}

		entryLines = append(entryLines, lineStr)
		currOffset += len(lineStr) + 1
	}

	p.processEntry(entryStartOffset, entryStartLine, entryLines)

	return scanner.Err()
}

func (p *fileProcessor) processEntry(startOffset, startLine int, lines []string) {
	if startLine <= 0 || len(lines) <= 0 {
		return
	}

	if p.run.EmitOrig == 1 {
		p.run.m.Lock()
		for _, line := range lines {
			fmt.Println(line)
		}
		p.run.m.Unlock()
	} else if p.run.EmitOrig == 2 {
		linesJoined := strings.Replace(strings.Join(lines, "\n"), "\n", " ", -1)
		p.run.m.Lock()
		fmt.Println(linesJoined)
		p.run.m.Unlock()
	}

	firstLine := lines[0]

	matchIndex := p.fmeta.PrefixRE.FindStringSubmatchIndex(firstLine)
	if len(matchIndex) <= 0 {
		return
	}

	ts := string(p.fmeta.PrefixRE.ExpandString(nil,
		"${year}-${month}-${day}T${HH}:${MM}:${SS}.${SSSS}", firstLine, matchIndex))
	if len(ts) > len("2016-04-19T23:10:31.209") {
		ts = ts[0:len("2016-04-19T23:10:31.209")]
	}

	module := string(p.fmeta.PrefixRE.ExpandString(nil, "${module}", firstLine, matchIndex))

	level := string(p.fmeta.PrefixRE.ExpandString(nil, "${level}", firstLine, matchIndex))
	level = strings.ToUpper(strings.Trim(level, "[]"))
	if len(level) > 4 {
		level = level[0:4]
	}

	lines[0] = firstLine[matchIndex[1]:] // Strip off PrefixRE's match.

	p.buf = p.buf[0:0]
	for _, line := range lines {
		p.buf = append(p.buf, []byte(line)...)
		p.buf = append(p.buf, '\n')
	}

	if p.fmeta.Cleanser != nil {
		p.buf = p.fmeta.Cleanser(p.buf)
	}

	var s scanner.Scanner // Use go's tokenizer to parse the entry.

	fset := token.NewFileSet()

	s.Init(fset.AddFile(p.dir+"/"+p.fname, fset.Base(),
		len(p.buf)), p.buf, nil /* No error handler. */, 0)

	p.processEntryTokens(startOffset, startLine, ts, module, level, &s,
		make([]string, 0, 20))
}

// levelDelta tells us how some tokens affect our "depth" of nesting.
var levelDelta = map[token.Token]int{
	token.LPAREN: 1,
	token.RPAREN: -1, // )
	token.LBRACK: 1,
	token.RBRACK: -1, // ]
	token.LBRACE: 1,
	token.RBRACE: -1, // }

	// When value is 0, it means don't change level or nesting depth,
	// and also don't merge into neighboring tokens.

	token.CHAR:   0,
	token.INT:    0,
	token.FLOAT:  0,
	token.STRING: 0,

	token.ADD:       0, // +
	token.SUB:       0, // -
	token.MUL:       0, // *
	token.QUO:       0, // /
	token.COLON:     0,
	token.COMMA:     0,
	token.PERIOD:    0,
	token.SEMICOLON: 0,
}

var skipToken = map[token.Token]bool{
	token.SHL: true, // <<
	token.SHR: true, // >>
}

func (p *fileProcessor) processEntryTokens(startOffset, startLine int,
	ts, module, level string, s *scanner.Scanner, path []string) {
	var tokLits []tokLit
	var emitted int

	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}

		if skipToken[tok] {
			continue
		}

		delta, deltaExists := levelDelta[tok]
		if delta > 0 {
			pathSub := path
			pathPart := nameFromTokLits(tokLits)
			if pathPart != "" {
				pathSub = append(pathSub, pathPart)
			}

			emitted = p.emitTokLits(startOffset, startLine, ts, module, level,
				path, tokLits, emitted)

			// Recurse on nested sub-level.
			p.processEntryTokens(startOffset, startLine, ts, module, level, s, pathSub)
		} else if delta < 0 {
			break // Return from nested sub-level recursion.
		} else {
			// If the token is merge'able with the previous token,
			// then merge.  For example, we can merge an IDENT that's
			// followed by a consecutive IDENT.
			if !deltaExists && len(tokLits) > 0 {
				tokLitPrev := tokLits[len(tokLits)-1]
				if !tokLitPrev.emitted {
					_, prevDeltaExists := levelDelta[tokLitPrev.tok]
					if !prevDeltaExists {
						tokLits[len(tokLits)-1].lit =
							tokenLitString(tokLitPrev.tok, tokLitPrev.lit) + " " +
								tokenLitString(tok, lit)

						continue
					}
				}
			}

			tokLits = append(tokLits, tokLit{tok, lit, false})
		}
	}

	p.emitTokLits(startOffset, startLine, ts, module, level, path, tokLits, emitted)
}

// emitTokLits invokes run.emit() on the tokens that haven't been
// emitted yet, along with heuristic preprocessing & cleanup, too.
func (p *fileProcessor) emitTokLits(startOffset, startLine int,
	ts, module, level string, path []string, tokLits []tokLit, startAt int) int {
	var s []string

	for i := startAt; i < len(tokLits); i++ {
		tokLit := tokLits[i]
		if tokLit.emitted {
			continue
		}
		tokLit.emitted = true

		tokStr := tokLit.tok.String()
		if p.run.emitTypes[tokStr] {
			strs := strings.Trim(strings.Join(s, " "), "\t\n .:,")
			p.run.emit(ts, module, level, p.dirBase, p.fname, p.fnameOut,
				startOffset, startLine, "STRS", path, "", "STRING", strs, true)

			s = nil

			name := cleanseName(nameFromTokLits(tokLits[0:i]))
			if name != "" {
				namePath := path
				if len(namePath) <= 0 {
					namePath = strings.Split(name, " ")
					name = namePath[len(namePath)-1]
					namePath = namePath[0 : len(namePath)-1]
				}

				if name != "" {
					p.dict.AddDictEntry(tokStr, name, tokLit.lit)
					p.run.emit(ts, module, level, p.dirBase, p.fname, p.fnameOut,
						startOffset, startLine, "NAME", namePath,
						name, tokStr, tokLit.lit, false)
				}
			}
		} else {
			s = append(s, tokenLitString(tokLit.tok, tokLit.lit))
		}
	}

	strs := strings.Trim(strings.Join(s, " "), "\t\n .:,")
	p.run.emit(ts, module, level, p.dirBase, p.fname, p.fnameOut,
		startOffset, startLine, "TAIL", path, "", "STRING", strs, true)

	return len(tokLits)
}

// nameFromTokLits returns the last IDENT or STRING from the tokLits,
// which the caller can use as a name.
func nameFromTokLits(tokLits []tokLit) string {
	for i := len(tokLits) - 1; i >= 0; i-- {
		tok := tokLits[i].tok
		if tok == token.IDENT || tok == token.STRING {
			return tokLits[i].lit
		}
	}
	return ""
}

// cleanseName cleans up a name string, using heuristic rules;
// otherwise, returns "" for an invalid name.
func cleanseName(name string) string {
	name = strings.Trim(name, " \t\n\"")
	if strings.IndexAny(name, "<>/ ") >= 0 ||
		name == "true" || name == "false" ||
		name == "ok" || name == "pid" || name == "uuid" ||
		strings.HasPrefix(name, "0x") || int_re.MatchString(name) {
		return ""
	}
	return name
}

func tokenLitString(tok token.Token, lit string) string {
	if lit != "" {
		return lit
	}
	return tok.String()
}
