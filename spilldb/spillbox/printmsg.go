//+build ignore

package main

import (
	"io"
	"log"
	"os"

	"crawshaw.io/iox"
	"spilled.ink/spilldb/spillbox"
)

func main() {
	sbox, err := spillbox.New(os.Args[1], 1)
	if err != nil {
		log.Fatal(err)
	}
	defer sbox.Close()

	msgID, err := spillbox.ParseMsgID(os.Args[2])
	if err != nil {
		log.Fatal(err)
	}

	conn := sbox.PoolRO.Get(nil)
	defer sbox.PoolRO.Put(conn)

	filer := iox.NewFiler(0)

	buf, err := spillbox.BuildMessage(conn, filer, msgID)
	if err != nil {
		log.Fatal(err)
	}
	io.Copy(os.Stdout, buf)
}
