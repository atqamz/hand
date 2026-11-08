package board

type startForm struct {
	Base, Token, Label, Starting string
	Inline, Primary              bool
}

type resumeForm struct {
	Base, Token, Starting string
}

type ackForm struct {
	Base, Token, Ref string
	Fetch            bool
}
