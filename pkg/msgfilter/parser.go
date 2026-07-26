package msgfilter

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxExprLen  = 256
	maxTermCount = 16
)

var errInvalidExpr = fmt.Errorf("invalid msgre expression")

// Parse parses a boolean keyword expression (without the msgre: prefix).
func Parse(expr string) (Node, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, errInvalidExpr
	}
	if len(expr) > maxExprLen {
		return nil, errInvalidExpr
	}
	p := &parser{input: expr}
	node, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.input) {
		return nil, errInvalidExpr
	}
	if countTerms(node) > maxTermCount {
		return nil, errInvalidExpr
	}
	return node, nil
}

type parser struct {
	input string
	pos   int
}

func (p *parser) parseExpr() (Node, error) {
	return p.parseOr()
}

func (p *parser) parseOr() (Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	children := []Node{left}
	for p.match('|') {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		children = append(children, right)
	}
	if len(children) == 1 {
		return children[0], nil
	}
	return &Or{Children: children}, nil
}

func (p *parser) parseAnd() (Node, error) {
	left, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	children := []Node{left}
	for p.match('&') {
		right, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		children = append(children, right)
	}
	if len(children) == 1 {
		return children[0], nil
	}
	return &And{Children: children}, nil
}

func (p *parser) parseNot() (Node, error) {
	if p.match('!') {
		child, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &Not{Child: child}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (Node, error) {
	p.skipSpace()
	if p.match('(') {
		node, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if !p.match(')') {
			return nil, errInvalidExpr
		}
		return node, nil
	}
	return p.parseTerm()
}

func (p *parser) parseTerm() (*Term, error) {
	p.skipSpace()
	if p.pos >= len(p.input) {
		return nil, errInvalidExpr
	}
	if p.input[p.pos] == '"' {
		return p.parseQuotedTerm()
	}
	start := p.pos
	for p.pos < len(p.input) {
		r, width := utf8.DecodeRuneInString(p.input[p.pos:])
		if r == '&' || r == '|' || r == '!' || r == '(' || r == ')' || unicode.IsSpace(r) {
			break
		}
		p.pos += width
	}
	if start == p.pos {
		return nil, errInvalidExpr
	}
	val := strings.TrimSpace(p.input[start:p.pos])
	if val == "" {
		return nil, errInvalidExpr
	}
	return &Term{Value: val}, nil
}

func (p *parser) parseQuotedTerm() (*Term, error) {
	if p.input[p.pos] != '"' {
		return nil, errInvalidExpr
	}
	p.pos++
	start := p.pos
	for p.pos < len(p.input) {
		if p.input[p.pos] == '"' {
			val := p.input[start:p.pos]
			p.pos++
			if val == "" {
				return nil, errInvalidExpr
			}
			return &Term{Value: val}, nil
		}
		_, width := utf8.DecodeRuneInString(p.input[p.pos:])
		p.pos += width
	}
	return nil, errInvalidExpr
}

func (p *parser) skipSpace() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func (p *parser) match(ch byte) bool {
	p.skipSpace()
	if p.pos < len(p.input) && p.input[p.pos] == ch {
		p.pos++
		return true
	}
	return false
}

func countTerms(node Node) int {
	switch n := node.(type) {
	case *Term:
		return 1
	case *Not:
		return countTerms(n.Child)
	case *And:
		var c int
		for _, ch := range n.Children {
			c += countTerms(ch)
		}
		return c
	case *Or:
		var c int
		for _, ch := range n.Children {
			c += countTerms(ch)
		}
		return c
	default:
		return 0
	}
}
