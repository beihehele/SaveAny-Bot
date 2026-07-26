package msgfilter

// Node is a boolean expression AST for keyword matching.
type Node interface{}

type Term struct {
	Value string
}

type Not struct {
	Child Node
}

type And struct {
	Children []Node
}

type Or struct {
	Children []Node
}
