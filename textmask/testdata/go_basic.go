package main

import (
	"fmt"
	"strings"
)

// Comment with 'apostrophe and "quote"
/* block comment
   func phantom() {}
*/
func fix(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	r := '\''
	q := '"'
	u := 'é'
	raw := `
func phantom2() {
	return "x"
}
`
	s := "escaped \" quote // not comment"
	fmt.Println(r, q, u, raw, s)
	return p
}
