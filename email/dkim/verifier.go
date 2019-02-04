//+build ignore

package main

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"

	"spilled.ink/email/dkim"
)

func main() {
	src := os.Stdin
	name := "stdin"
	if len(os.Args) == 2 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(1)
		}
		src = f
		name = os.Args[1]
	}

	email, err := ioutil.ReadAll(src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v", err)
		os.Exit(2)
	}

	v := dkim.Verifier{}
	if err := v.Verify(context.Background(), bytes.NewReader(email)); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %s\n", name, err)
		os.Exit(1)
	}
	fmt.Println("PASS")
}
