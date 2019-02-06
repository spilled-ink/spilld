package spillbox

import (
	"math/rand"

	"crawshaw.io/sqlite"
	"spilled.ink/email"
)

// ResolveAddressID computes a DB AddressID and ContactID for an email address.
//
// If no existing contact record is found, one is created.
// Address normalization is used to match new addresses to existing
// contacts if it is possible.
func ResolveAddressID(conn *sqlite.Conn, addr *email.Address, visible bool) (addressID AddressID, contactID ContactID, err error) {
	var visibleInDB bool

	stmt := conn.Prep("SELECT AddressID, ContactID, Visible FROM Addresses WHERE Name = $name AND Address = $addr;")
	stmt.SetText("$name", addr.Name)
	stmt.SetText("$addr", addr.Addr)
	if hasNext, err := stmt.Step(); err != nil {
		return 0, 0, err
	} else if hasNext {
		addressID = AddressID(stmt.GetInt64("AddressID"))
		contactID = ContactID(stmt.GetInt64("ContactID"))
		visibleInDB = stmt.GetInt64("Visible") > 0
		stmt.Reset()
	}

	if contactID == 0 {
		// Try to find a contact with a name variant.
		stmt := conn.Prep("SELECT ContactID FROM Addresses WHERE Address = $addr;")
		stmt.SetText("$addr", addr.Addr)
		if hasNext, err := stmt.Step(); err != nil {
			return 0, 0, err
		} else if hasNext {
			contactID = ContactID(stmt.GetInt64("ContactID"))
			stmt.Reset()
		}
	}

	normAddr := string(normalizeAddr([]byte(addr.Addr)))
	if contactID == 0 {
		// Try to find a contact with a normalized addr.
		stmt := conn.Prep("SELECT ContactID FROM Addresses WHERE Address = $addr;")
		stmt.SetText("$addr", normAddr)
		if hasNext, err := stmt.Step(); err != nil {
			return 0, 0, err
		} else if hasNext {
			contactID = ContactID(stmt.GetInt64("ContactID"))
			stmt.Reset()
		}
	}

	defaultAddr := false
	if contactID == 0 {
		// We have never seen the address before or its like, so make a new contact.
		// TODO: take an initially better guess at Robot?
		stmt := conn.Prep("INSERT INTO Contacts (ContactID, Robot) VALUES ($contactID, FALSE);")
		if id, err := InsertRandID(stmt, "$contactID"); err != nil {
			return 0, 0, err
		} else {
			contactID = ContactID(id)
		}
		defaultAddr = true
	}

	if addressID == 0 {
		// New addr (even if a non-normal variant), add for the contact.
		stmt := conn.Prep(`INSERT INTO Addresses (AddressID, ContactID, Name, Address, DefaultAddr, Visible) 
			VALUES ($addressID, $contactID, $name, $addr, $defaultAddr, $visible);`)
		stmt.SetInt64("$contactID", int64(contactID))
		stmt.SetText("$name", addr.Name)
		stmt.SetText("$addr", addr.Addr)
		stmt.SetBool("$visible", visible)
		stmt.SetBool("$defaultAddr", defaultAddr)
		if id, err := InsertRandID(stmt, "$addressID"); err != nil {
			return 0, 0, err
		} else {
			addressID = AddressID(id)
		}
		visibleInDB = visible
		stmt.Reset()

		if normAddr != addr.Addr {
			stmt.SetText("$addr", normAddr)
			stmt.SetBool("$visible", false)
			stmt.SetBool("$defaultAddr", false)
			if _, err := InsertRandID(stmt, "$addressID"); err != nil {
				return 0, 0, err
			}
			stmt.Reset()
		}

		fallbackPicID := rand.Int63n(1000)

		stmt = conn.Prep(`INSERT INTO ProfilePics (PicID, AddressID, FetchTime, ContactID, FallbackPicID)
			VALUES ($picID, $addressID, $fetchTime, $contactID, $fallbackPicID);`)
		stmt.SetInt64("$addressID", int64(addressID))
		stmt.SetInt64("$contactID", int64(contactID))
		stmt.SetInt64("$fetchTime", 0)
		stmt.SetInt64("$fallbackPicID", fallbackPicID)
		if _, err := InsertRandIDMin(stmt, "$picID", 1001); err != nil {
			return 0, 0, err
		}
	}

	if visible && !visibleInDB {
		stmt := conn.Prep("UPDATE Addresses SET Visible = $visible WHERE AddressID = $addressID;")
		stmt.SetBool("$visible", visible)
		stmt.SetInt64("$addressID", int64(addressID))
		if _, err := stmt.Step(); err != nil {
			return 0, 0, err
		}
	}

	return addressID, contactID, nil
}
