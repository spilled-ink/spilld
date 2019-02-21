// The spillbox command is a command-line tool for managing a spilldb database.
//
// TODO:
//	spillbox users 			- list users
//	spillbox users add 		- add a new user
//	spillbox user [username] 	- print user summary
//	spillbox user [username] gc	- garbage collect and vacuum
//	spillbox user [username] import [path to mbox, maildir, or spillbox]
//	spillbox user [username] printmsg [msgid]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"crawshaw.io/iox"
	"spilled.ink/spilldb"
	"spilled.ink/spilldb/boxmgmt"
)

var filer *iox.Filer
var sdb *spilldb.Server

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [-dbdir path] [command]\nRun '%s help' for details.\n\n", os.Args[0], os.Args[0])
		flag.PrintDefaults()
	}
	// TODO: default location in data storage directory for a user.
	flagDBDir := flag.String("dbdir", "", "spilldb database directory")
	flagVerbose := flag.Bool("verbose", false, "verbose logging")
	flag.Parse()

	if len(flag.Args()) == 0 {
		flag.Usage()
		exit(2)
	}

	ctx := context.Background()
	filer = iox.NewFiler(0)

	// TODO: print a message if we are creating dbdir
	var err error
	sdb, err = spilldb.New(filer, *flagDBDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", os.Args[0], err)
		exit(2)
	}
	if !*flagVerbose {
		sdb.Logf = func(format string, v ...interface{}) {} // drop
	}

	switch flag.Arg(0) {
	default:
		fmt.Fprintf(os.Stderr, "%s: unknown command '%s'\nRun '%s help' for details.\n", os.Args[0], flag.Arg(0), os.Args[0])
		exit(1)
	case "users":
		if err := listUsers(); err != nil {
			fmt.Fprintf(os.Stderr, "%s users: %v\n", os.Args[0], err)
			exit(1)
		}
	case "user":
		if len(flag.Args()) < 2 {
			fmt.Fprintf(os.Stderr, "usage: %s [-dbdir path] user [userid or username] [user-command]\nRun '%s help user' for details.\n", os.Args[0], os.Args[0])
			exit(2)
		}
		var u *boxmgmt.User
		userID, err := strconv.ParseInt(flag.Arg(1), 10, 64)
		if err != nil {
			userID, err = findUserID(flag.Arg(1))
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s user: %v\n", os.Args[0], err)
				exit(1)
			}
		}
		u, err = sdb.BoxMgmt.Open(ctx, userID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s user: cannot find user ID %d: %v\n", os.Args[0], userID, err)
			exit(1)
		}
		_ = u

		if len(flag.Args()) == 2 {
			fmt.Printf("TODO print summary of user %d\n", userID)
			exit(0)
		}

		switch flag.Arg(2) {
		default:
			fmt.Fprintf(os.Stderr, "%s user: unknown command '%s'\nRun '%s user help' for details.\n", os.Args[0], flag.Arg(2), os.Args[0])
			exit(1)
		case "import":
			if len(flag.Args()) != 4 {
				fmt.Fprintf(os.Stderr, "usage: %s user [userid] import [path to sources]\n", os.Args[0])
				exit(1)
			}
			if err := importData(u, flag.Arg(3)); err != nil {
				fmt.Fprintf(os.Stderr, "%s user data import: %v\n", os.Args[0], err)
				exit(1)
			}
			fmt.Printf("TODO import\n")
			exit(0)
		}
	}

	exit(0)
}

func listUsers() error {
	conn := sdb.DB.Get(nil)
	defer sdb.DB.Put(conn)
	stmt := conn.Prep("SELECT UserID, Address FROM UserAddresses WHERE PrimaryAddr IS TRUE ORDER BY UserID;")
	fmt.Fprintf(os.Stdout, "UserID\tAddress\n")
	for {
		if hasNext, err := stmt.Step(); err != nil {
			return err
		} else if !hasNext {
			break
		}
		fmt.Fprintf(os.Stdout, "%d\t%s\n", stmt.GetInt64("UserID"), stmt.GetText("Address"))
	}

	return nil
}

func importData(u *boxmgmt.User, sourcePath string) error {
	return fmt.Errorf("TODO")
}

func findUserID(username string) (int64, error) {
	conn := sdb.DB.Get(nil)
	defer sdb.DB.Put(conn)
	stmt := conn.Prep("SELECT UserID FROM UserAddresses WHERE Address = $username")
	stmt.SetText("$username", username)
	if hasNext, err := stmt.Step(); err != nil {
		return 0, fmt.Errorf("searching for username: %v\n", err)
	} else if !hasNext {
		return 0, fmt.Errorf("cannot find user %q\n", username)
	}
	userID := stmt.GetInt64("UserID")
	stmt.Reset()
	return userID, nil
}

func exit(code int) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	if sdb != nil {
		if err := sdb.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "%s: spilldb shutdown error: %v\n", os.Args[0], err)
		}
	}
	if filer != nil {
		if err := filer.Shutdown(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "%s: filer shutdown error: %v\n", os.Args[0], err)
		}
	}
	os.Exit(code)
}
