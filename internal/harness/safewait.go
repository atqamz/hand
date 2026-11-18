package harness

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	sleepArg = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[smhd]?$`)
	loopName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	seqSub   = regexp.MustCompile(`^\$\(seq( -?[0-9]+){1,3}\)$`)
)

type shellTok struct {
	op     string
	text   string
	quoted bool
}

func KnownWait(command string) bool {
	toks, ok := shellTokens(command)
	if !ok {
		return false
	}
	p := &shellParse{toks: toks}
	return p.list("") && p.commands > 0
}

type shellLex struct {
	out    []shellTok
	cur    strings.Builder
	in     bool
	quoted bool
	redir  string
	wait   bool
}

func (l *shellLex) flush() bool {
	if !l.in {
		return true
	}
	text, quoted := l.cur.String(), l.quoted
	l.cur.Reset()
	l.in, l.quoted = false, false
	if l.wait {
		l.wait = false
		if l.redir == ">&" {
			return text == "1" || text == "2"
		}
		return text == "/dev/null"
	}
	l.out = append(l.out, shellTok{text: text, quoted: quoted})
	return true
}

func (l *shellLex) redirect(op string) bool {
	if l.in && strings.Trim(l.cur.String(), "0123456789") == "" && !l.quoted {
		l.cur.Reset()
		l.in = false
	}
	if !l.flush() || l.wait {
		return false
	}
	l.redir, l.wait = op, true
	return true
}

func (l *shellLex) operator(op string) bool {
	if !l.flush() || l.wait {
		return false
	}
	l.out = append(l.out, shellTok{op: op})
	return true
}

func shellTokens(src string) ([]shellTok, bool) {
	var l shellLex
	r := []rune(src)
	at := func(i int) rune {
		if i < len(r) {
			return r[i]
		}
		return 0
	}
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch c {
		case '\\':
			if at(i+1) == 0 {
				return nil, false
			}
			i++
			if r[i] == '\n' {
				continue
			}
			l.in, l.quoted = true, true
			l.cur.WriteRune(r[i])
		case '\'':
			j := i + 1
			for j < len(r) && r[j] != '\'' {
				j++
			}
			if j == len(r) {
				return nil, false
			}
			l.in, l.quoted = true, true
			l.cur.WriteString(string(r[i+1 : j]))
			i = j
		case '"':
			j := i + 1
			for j < len(r) && r[j] != '"' {
				switch {
				case r[j] == '\\':
					j++
				case r[j] == '`', r[j] == '$' && !shellNameStart(at(j+1)):
					return nil, false
				}
				j++
			}
			if j >= len(r) {
				return nil, false
			}
			l.in, l.quoted = true, true
			l.cur.WriteString(string(r[i+1 : j]))
			i = j
		case '$':
			if at(i+1) == '(' && !l.in {
				j := i
				for j < len(r) && r[j] != ')' {
					j++
				}
				if j == len(r) || !seqSub.MatchString(string(r[i:j+1])) {
					return nil, false
				}
				l.in = true
				l.cur.WriteString(string(r[i : j+1]))
				i = j
				continue
			}
			if !shellNameStart(at(i + 1)) {
				return nil, false
			}
			l.in = true
			l.cur.WriteRune(c)
		case ' ', '\t', '\r':
			if !l.flush() {
				return nil, false
			}
		case '#':
			if l.in {
				l.cur.WriteRune(c)
				continue
			}
			for at(i+1) != 0 && at(i+1) != '\n' {
				i++
			}
		case '>':
			op := ">"
			switch at(i + 1) {
			case '>':
				op, i = ">>", i+1
			case '&':
				op, i = ">&", i+1
			case '|':
				return nil, false
			}
			if !l.redirect(op) {
				return nil, false
			}
		case '&':
			switch at(i + 1) {
			case '>':
				i++
				if !l.redirect("&>") {
					return nil, false
				}
			case '&':
				i++
				if !l.operator("&&") {
					return nil, false
				}
			default:
				return nil, false
			}
		case '|':
			op := "|"
			switch at(i + 1) {
			case '|':
				op, i = "||", i+1
			case '&':
				return nil, false
			}
			if !l.operator(op) {
				return nil, false
			}
		case ';':
			if at(i+1) == ';' || !l.operator(";") {
				return nil, false
			}
		case '\n':
			if !l.operator("\n") {
				return nil, false
			}
		case '<', '(', ')', '`':
			return nil, false
		default:
			l.in = true
			l.cur.WriteRune(c)
		}
	}
	if !l.flush() || l.wait {
		return nil, false
	}
	return l.out, true
}

func shellNameStart(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

type shellParse struct {
	toks     []shellTok
	i        int
	commands int
}

func (p *shellParse) word() (shellTok, bool) {
	if p.i < len(p.toks) && p.toks[p.i].op == "" {
		t := p.toks[p.i]
		p.i++
		return t, true
	}
	return shellTok{}, false
}

func (p *shellParse) reserved(text string) bool {
	t := p.peek()
	return t.op == "" && !t.quoted && t.text == text && p.i < len(p.toks)
}

func (p *shellParse) peek() shellTok {
	if p.i < len(p.toks) {
		return p.toks[p.i]
	}
	return shellTok{}
}

func (p *shellParse) separator() bool {
	op := p.peek().op
	return op == ";" || op == "\n"
}

func (p *shellParse) skip() {
	for p.separator() {
		p.i++
	}
}

func (p *shellParse) list(stop string) bool {
	for {
		p.skip()
		switch {
		case stop != "" && p.reserved(stop):
			return true
		case p.i >= len(p.toks):
			return stop == ""
		case !p.command():
			return false
		}
		for op := p.peek().op; op == "&&" || op == "||" || op == "|"; op = p.peek().op {
			p.i++
			if !p.command() {
				return false
			}
		}
		if p.i < len(p.toks) && !p.separator() && !(stop != "" && p.reserved(stop)) {
			return false
		}
	}
}

func (p *shellParse) command() bool {
	if p.reserved("for") {
		return p.loop()
	}
	var words []string
	for {
		w, ok := p.word()
		if !ok {
			break
		}
		if len(words) == 0 && !w.quoted && (w.text == "do" || w.text == "done" || w.text == "in") {
			return false
		}
		words = append(words, w.text)
	}
	if !knownWaitWords(words) {
		return false
	}
	p.commands++
	return true
}

func (p *shellParse) loop() bool {
	p.i++
	name, ok := p.word()
	if !ok || name.quoted || !loopName.MatchString(name.text) || !p.reserved("in") {
		return false
	}
	p.i++
	for {
		if _, ok := p.word(); !ok {
			break
		}
	}
	if !p.separator() {
		return false
	}
	p.skip()
	if !p.reserved("do") {
		return false
	}
	p.i++
	before := p.commands
	if !p.list("done") || p.commands == before {
		return false
	}
	p.i++
	return true
}

func knownWaitWords(w []string) bool {
	switch {
	case len(w) == 0:
		return false
	case w[0] == "sleep":
		return len(w) == 2 && sleepArg.MatchString(w[1])
	case w[0] == "gh":
		return len(w) >= 3 && (w[1] == "pr" && w[2] == "checks" || w[1] == "run" && (w[2] == "view" || w[2] == "watch"))
	case w[0] == "hand" || filepath.IsAbs(w[0]) && filepath.Base(w[0]) == "hand" && !strings.Contains(w[0], "$"):
		return len(w) >= 2 && w[1] == "wait"
	}
	return false
}
