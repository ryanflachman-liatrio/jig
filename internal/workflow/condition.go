package workflow

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

const (
	MaxConditionDepth = 32
	MaxConditionNodes = 256
)

// CondOp is an operation in a guard expression.
type CondOp string

const (
	CondTruthy CondOp = "truthy"
	CondEq     CondOp = "=="
	CondNeq    CondOp = "!="
	CondLT     CondOp = "<"
	CondLTE    CondOp = "<="
	CondGT     CondOp = ">"
	CondGTE    CondOp = ">="
	CondAnd    CondOp = "&&"
	CondOr     CondOp = "||"
)

type ConditionRef struct {
	Step  string
	Field []string
}

type ConditionLiteral struct {
	Value  string
	Quoted bool
}

type ConditionExpr struct {
	Op          CondOp
	Left, Right *ConditionExpr
	Ref         ConditionRef
	Literal     ConditionLiteral
}

type Condition struct {
	Raw  string
	Root *ConditionExpr
}

// Predicates returns condition leaves in source order.
func (c *Condition) Predicates() []*ConditionExpr {
	if c == nil {
		return nil
	}
	var out []*ConditionExpr
	var walk func(*ConditionExpr)
	walk = func(expr *ConditionExpr) {
		if expr == nil {
			return
		}
		if expr.Op == CondAnd || expr.Op == CondOr {
			walk(expr.Left)
			walk(expr.Right)
			return
		}
		out = append(out, expr)
	}
	walk(c.Root)
	return out
}

// ReferencedSteps returns stable, de-duplicated step IDs.
func (c *Condition) ReferencedSteps() []string {
	seen := map[string]bool{}
	var out []string
	for _, predicate := range c.Predicates() {
		if !seen[predicate.Ref.Step] {
			seen[predicate.Ref.Step] = true
			out = append(out, predicate.Ref.Step)
		}
	}
	return out
}

// RewriteRefs applies fn to every leaf and returns canonical expression text.
func (c *Condition) RewriteRefs(fn func(ConditionRef) (ConditionRef, error)) (string, error) {
	if c == nil || c.Root == nil {
		return "", nil
	}
	root := cloneConditionExpr(c.Root)
	var walk func(*ConditionExpr) error
	walk = func(expr *ConditionExpr) error {
		if expr.Op == CondAnd || expr.Op == CondOr {
			if err := walk(expr.Left); err != nil {
				return err
			}
			return walk(expr.Right)
		}
		ref, err := fn(expr.Ref)
		if err != nil {
			return err
		}
		expr.Ref = ref
		return nil
	}
	if err := walk(root); err != nil {
		return "", err
	}
	return formatConditionExpr(root, 0), nil
}

func (c *Condition) String() string {
	if c == nil {
		return ""
	}
	return formatConditionExpr(c.Root, 0)
}

func (c *Condition) canonicalKey() string {
	root := cloneConditionExpr(c.Root)
	var normalize func(*ConditionExpr)
	normalize = func(expr *ConditionExpr) {
		if expr == nil {
			return
		}
		if expr.Op == CondAnd || expr.Op == CondOr {
			normalize(expr.Left)
			normalize(expr.Right)
			return
		}
		expr.Literal.Quoted = true
	}
	normalize(root)
	return formatConditionExpr(root, 0)
}

func cloneConditionExpr(expr *ConditionExpr) *ConditionExpr {
	if expr == nil {
		return nil
	}
	clone := *expr
	clone.Ref.Field = append([]string(nil), expr.Ref.Field...)
	clone.Left = cloneConditionExpr(expr.Left)
	clone.Right = cloneConditionExpr(expr.Right)
	return &clone
}

func conditionPrecedence(op CondOp) int {
	switch op {
	case CondOr:
		return 1
	case CondAnd:
		return 2
	default:
		return 3
	}
}

func formatConditionExpr(expr *ConditionExpr, parentPrecedence int) string {
	if expr == nil {
		return ""
	}
	precedence := conditionPrecedence(expr.Op)
	var result string
	if expr.Op == CondAnd || expr.Op == CondOr {
		result = formatConditionExpr(expr.Left, precedence) + " " + string(expr.Op) + " " + formatConditionExpr(expr.Right, precedence)
	} else {
		result = expr.Ref.Step
		if len(expr.Ref.Field) > 0 {
			result += "." + strings.Join(expr.Ref.Field, ".")
		}
		if expr.Op != CondTruthy {
			result += " " + string(expr.Op) + " " + formatConditionLiteral(expr.Literal)
		}
	}
	if precedence < parentPrecedence {
		return "(" + result + ")"
	}
	return result
}

func formatConditionLiteral(lit ConditionLiteral) string {
	if !lit.Quoted && (lit.Value == "true" || lit.Value == "false" || isConditionNumber(lit.Value)) {
		return lit.Value
	}
	return strconv.Quote(lit.Value)
}

type conditionTokenKind uint8

const (
	tokenEOF conditionTokenKind = iota
	tokenBare
	tokenString
	tokenLParen
	tokenRParen
	tokenOp
)

type conditionToken struct {
	kind   conditionTokenKind
	text   string
	offset int
}

type conditionLexer struct {
	raw string
	pos int
}

func (l *conditionLexer) next() (conditionToken, error) {
	for l.pos < len(l.raw) && unicode.IsSpace(rune(l.raw[l.pos])) {
		l.pos++
	}
	if l.pos >= len(l.raw) {
		return conditionToken{kind: tokenEOF, offset: l.pos}, nil
	}
	start := l.pos
	switch l.raw[l.pos] {
	case '(':
		l.pos++
		return conditionToken{kind: tokenLParen, text: "(", offset: start}, nil
	case ')':
		l.pos++
		return conditionToken{kind: tokenRParen, text: ")", offset: start}, nil
	case '\'', '"':
		quote := l.raw[l.pos]
		l.pos++
		for l.pos < len(l.raw) {
			ch := l.raw[l.pos]
			if ch == quote {
				l.pos++
				return conditionToken{kind: tokenString, text: decodeConditionString(l.raw[start:l.pos], quote), offset: start}, nil
			}
			if ch == '\\' && l.pos+1 < len(l.raw) {
				l.pos += 2
				continue
			}
			l.pos++
		}
		return conditionToken{}, fmt.Errorf("condition syntax error at offset %d: unterminated quote", start)
	case '&', '|':
		ch := l.raw[l.pos]
		if l.pos+1 >= len(l.raw) || l.raw[l.pos+1] != ch {
			return conditionToken{}, fmt.Errorf("condition syntax error at offset %d near %q: use %c%c", start, string(ch), ch, ch)
		}
		l.pos += 2
		return conditionToken{kind: tokenOp, text: string([]byte{ch, ch}), offset: start}, nil
	case '=', '!':
		ch := l.raw[l.pos]
		if l.pos+1 >= len(l.raw) || l.raw[l.pos+1] != '=' {
			return conditionToken{}, fmt.Errorf("condition syntax error at offset %d near %q: expected %c=", start, string(ch), ch)
		}
		l.pos += 2
		return conditionToken{kind: tokenOp, text: string([]byte{ch, '='}), offset: start}, nil
	case '<', '>':
		l.pos++
		if l.pos < len(l.raw) && l.raw[l.pos] == '=' {
			l.pos++
		}
		return conditionToken{kind: tokenOp, text: l.raw[start:l.pos], offset: start}, nil
	}
	for l.pos < len(l.raw) && !unicode.IsSpace(rune(l.raw[l.pos])) && !strings.ContainsRune("()&|=!<>'\"", rune(l.raw[l.pos])) {
		l.pos++
	}
	if l.pos == start {
		return conditionToken{}, fmt.Errorf("condition syntax error at offset %d near %q", start, string(l.raw[l.pos]))
	}
	return conditionToken{kind: tokenBare, text: l.raw[start:l.pos], offset: start}, nil
}

func decodeConditionString(quoted string, quote byte) string {
	if quote == '"' {
		if value, err := strconv.Unquote(quoted); err == nil {
			return value
		}
	}
	content := quoted[1 : len(quoted)-1]
	var b strings.Builder
	for i := 0; i < len(content); i++ {
		if content[i] == '\\' && i+1 < len(content) && (content[i+1] == quote || content[i+1] == '\\') {
			i++
		}
		b.WriteByte(content[i])
	}
	return b.String()
}

type conditionParser struct {
	lexer  conditionLexer
	peeked *conditionToken
	nodes  int
}

func ParseCondition(raw string) (*Condition, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("empty condition")
	}
	p := &conditionParser{lexer: conditionLexer{raw: raw}}
	root, err := p.parseOr(0)
	if err != nil {
		return nil, err
	}
	tok, err := p.peek()
	if err != nil {
		return nil, err
	}
	if tok.kind != tokenEOF {
		return nil, p.errorf(tok, "unexpected trailing token %q", tok.text)
	}
	return &Condition{Raw: raw, Root: root}, nil
}

func (p *conditionParser) parseOr(depth int) (*ConditionExpr, error) {
	left, err := p.parseAnd(depth)
	if err != nil {
		return nil, err
	}
	for {
		tok, err := p.peek()
		if err != nil || tok.kind != tokenOp || tok.text != string(CondOr) {
			return left, err
		}
		p.take()
		right, err := p.parseAnd(depth)
		if err != nil {
			return nil, err
		}
		left, err = p.node(&ConditionExpr{Op: CondOr, Left: left, Right: right}, tok)
		if err != nil {
			return nil, err
		}
	}
}

func (p *conditionParser) parseAnd(depth int) (*ConditionExpr, error) {
	left, err := p.parsePrimary(depth)
	if err != nil {
		return nil, err
	}
	for {
		tok, err := p.peek()
		if err != nil || tok.kind != tokenOp || tok.text != string(CondAnd) {
			return left, err
		}
		p.take()
		right, err := p.parsePrimary(depth)
		if err != nil {
			return nil, err
		}
		left, err = p.node(&ConditionExpr{Op: CondAnd, Left: left, Right: right}, tok)
		if err != nil {
			return nil, err
		}
	}
}

func (p *conditionParser) parsePrimary(depth int) (*ConditionExpr, error) {
	if depth > MaxConditionDepth {
		return nil, fmt.Errorf("condition exceeds maximum nesting depth %d", MaxConditionDepth)
	}
	tok, err := p.peek()
	if err != nil {
		return nil, err
	}
	if tok.kind == tokenLParen {
		p.take()
		expr, err := p.parseOr(depth + 1)
		if err != nil {
			return nil, err
		}
		close, err := p.peek()
		if err != nil {
			return nil, err
		}
		if close.kind != tokenRParen {
			return nil, p.errorf(close, "expected )")
		}
		p.take()
		return expr, nil
	}
	return p.parsePredicate()
}

func (p *conditionParser) parsePredicate() (*ConditionExpr, error) {
	refToken, err := p.peek()
	if err != nil {
		return nil, err
	}
	if refToken.kind != tokenBare {
		if refToken.kind == tokenEOF {
			return nil, p.errorf(refToken, "missing operand")
		}
		return nil, p.errorf(refToken, "expected reference, got %q", refToken.text)
	}
	p.take()
	step, field, err := parseCondRef(refToken.text)
	if err != nil {
		return nil, p.errorf(refToken, "%v", err)
	}
	expr := &ConditionExpr{Op: CondTruthy, Ref: ConditionRef{Step: step, Field: field}}
	op, err := p.peek()
	if err != nil {
		return nil, err
	}
	if op.kind != tokenOp || (op.text != "==" && op.text != "!=" && op.text != "<" && op.text != "<=" && op.text != ">" && op.text != ">=") {
		return p.node(expr, refToken)
	}
	p.take()
	literal, err := p.peek()
	if err != nil {
		return nil, err
	}
	if literal.kind != tokenBare && literal.kind != tokenString {
		return nil, p.errorf(literal, "expected literal after %s", op.text)
	}
	p.take()
	expr.Op = CondOp(op.text)
	expr.Literal = ConditionLiteral{Value: literal.text, Quoted: literal.kind == tokenString}
	return p.node(expr, refToken)
}

func (p *conditionParser) node(expr *ConditionExpr, tok conditionToken) (*ConditionExpr, error) {
	p.nodes++
	if p.nodes > MaxConditionNodes {
		return nil, p.errorf(tok, "condition exceeds maximum syntax nodes %d", MaxConditionNodes)
	}
	return expr, nil
}

func (p *conditionParser) peek() (conditionToken, error) {
	if p.peeked != nil {
		return *p.peeked, nil
	}
	tok, err := p.lexer.next()
	if err != nil {
		return conditionToken{}, err
	}
	p.peeked = &tok
	return tok, nil
}

func (p *conditionParser) take() {
	p.peeked = nil
}

func (p *conditionParser) errorf(tok conditionToken, format string, args ...any) error {
	return fmt.Errorf("condition syntax error at offset %d: %s", tok.offset, fmt.Sprintf(format, args...))
}

func parseCondRef(s string) (step string, field []string, err error) {
	step, field = parseRef(s)
	if !isIdent(step) {
		return "", nil, fmt.Errorf("%q is not a step id", s)
	}
	for _, seg := range field {
		if !isIdent(seg) {
			return "", nil, fmt.Errorf("%q has an invalid field segment %q", s, seg)
		}
	}
	return step, field, nil
}

func isConditionNumber(s string) bool {
	if !jsonNumberSyntax(s) {
		return false
	}
	n, err := strconv.ParseFloat(s, 64)
	return err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}

func jsonNumberSyntax(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	if i >= len(s) {
		return false
	}
	if s[i] == '0' {
		i++
	} else if s[i] >= '1' && s[i] <= '9' {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	} else {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return false
		}
	}
	return i == len(s)
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
