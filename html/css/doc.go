/*
Package css implements a CSS tokenizer and parser.
The parser is currently incomplete, it only covers declaration lists.

It is written to the CSS Syntax Module Level 3 specification,
https://www.w3.org/TR/css-syntax-3/.
There are oddities in the spec so it is not taken as gospel.
It suggests for example that declarations in style attributes
can contain at-rules, when all other sources and implementations
say they cannot.
So this package was written by also consulting other sources,
such as https://developer.mozilla.org/en-US/docs/Web/CSS/Syntax.

Scanner

Turn bytes into tokens by calling the Next method until an EOF token:

	errh := func(line, col, n int, msg string) {
		log.Printf("%d:%d: %s", line, col, msg)
	}
	s := css.NewScanner(r, errh)
	for {
		s.Next()
		if s.Token == css.EOF {
			break
		}
		// ... process the token fields of s.
	}

The error handler function errh will be called for CSS tokenization
errors and any underlying I/O errors from the provided io.Reader.

Note: []byte data provided by s is reused when Next is called.

Parser

An example of parsing a style attribute:

	errh := func(line, col, n int, msg string) {
		log.Printf("%d:%d: %s", line, col, msg)
	}
	p := css.NewParser(css.NewScanner(r, errh))
	var decl css.Decl
	for p.ParseDecl(&decl) {
		// A declaration is written to decl
		// and any parse errors are reported to errh.
	}

*/
package css
