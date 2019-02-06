package spillbox

const createSQL = `
-- SQL schema for a spilldb single user mailbox, a.k.a. a spillbox.
--
-- Contains a complete copy of a user email archive,
-- including any active draft messages.
--
-- Synced to the cloud and then synced down other devices.
-- Some tables have special handling and are not synced,
-- those are documented in comments below.

PRAGMA journal_mode=WAL;

-- For IMAP XAPPLEPUSHSERVICE.
CREATE TABLE IF NOT EXISTS ApplePushDevices (
	Mailbox          TEXT NOT NULL,
	AppleAccountID   TEXT NOT NULL,
	AppleDeviceToken TEXT NOT NULL
);

-- Contacts is a list of contacts.
-- It is generated from incoming email and curated by users.
-- ContactID == 1 is always the user of this account.
CREATE TABLE IF NOT EXISTS Contacts (
	ContactID   INTEGER PRIMARY KEY,
	Hidden      BOOLEAN, -- removed by user but still represents stored email
	Robot       BOOLEAN  -- not a human
);

-- Note: AddressID is used in messages under the assumption that Name/Address
-- do not change. For contact edits, create a new AddressID.
CREATE TABLE IF NOT EXISTS Addresses (
	AddressID   INTEGER PRIMARY KEY,
	ContactID   INTEGER,
	Name        TEXT,             -- display name component of address
	Address     TEXT NOT NULL,    -- address: user@domain
	DefaultAddr BOOLEAN NOT NULL, -- default address for this contact
	Visible     BOOLEAN NOT NULL, -- user has sent or mentioned this address

	FOREIGN KEY(ContactID) REFERENCES Contacts(ContactID)
);

CREATE TABLE IF NOT EXISTS ProfilePics (
	-- The first 1000 PicIDs are reserved for fallback pics.
	PicID         INTEGER PRIMARY KEY,
	AddressID     INTEGER NOT NULL,
	FetchTime     INTEGER NOT NULL, -- 0 means a fetch is pending
	ContactID     INTEGER NOT NULL, -- TODO remove

	-- For a typical profile pic either Content or FallbackPicID is set.
	-- A FallbackPicID is always less than 1000.
	-- For a PicID <= 1000, both are NULL.
	FallbackPicID INTEGER,
	Content       BLOB,

	UNIQUE (AddressID, FetchTime),
	FOREIGN KEY(AddressID) REFERENCES Addresses(AddressID),
	FOREIGN KEY(ContactID) REFERENCES Contacts(ContactID)
);

-- Tie the mod-sequence used by CONDSTORE to the mailbox name.
--
-- MailboxID is not visible to IMAP, so reusing a deleted
-- mailbox's name will stomp on its old values.
-- In theory we handle this by incrementing UIDValidity, as
-- message uniqueness in IMAP is determined by:
--	(mailbox name, UIDVALIDITY, UID)
-- but that is not explicitly mentioned in RFC 7162 for
-- mod-sequences, so we play it safe and always increment
-- the value for a given mailbox name.
CREATE TABLE IF NOT EXISTS MailboxSequencing (
	Name            TEXT PRIMARY KEY,
	NextModSequence INTEGER NOT NULL  -- uint32, IMAP RFC 7162 CONDSTORE
);

CREATE TABLE IF NOT EXISTS Mailboxes (
	MailboxID       INTEGER PRIMARY KEY,
	NextUID         INTEGER NOT NULL, -- uint32, used by IMAP
	UIDValidity     INTEGER NOT NULL, -- incremented on rename or create with old name
	Attrs           INTEGER, -- imapserver.ListAttrFlag
	Name            TEXT,
	DeletedName     TEXT,    -- Old label name before deletion
	Subscribed      BOOLEAN,

	UNIQUE(Name)
);

CREATE INDEX IF NOT EXISTS MailboxesName ON Mailboxes (Name);

CREATE TABLE IF NOT EXISTS Labels (
	LabelID     INTEGER PRIMARY KEY,
	Label       TEXT,    -- NULL means the label is deleted

	UNIQUE(Label)
);

CREATE INDEX IF NOT EXISTS LabelsLabel ON Labels (Label);

CREATE TABLE IF NOT EXISTS Convos (
	ConvoID      INTEGER PRIMARY KEY,
	ConvoSummary TEXT     -- JSON encoding of mdb.ConvoSummary
);

CREATE TABLE IF NOT EXISTS ConvoContacts (
	ConvoID      INTEGER,
	ContactID    INTEGER,

	PRIMARY KEY(ConvoID, ContactID),
	FOREIGN KEY(ConvoID)   REFERENCES Convos(ConvoID),
	FOREIGN KEY(ContactID) REFERENCES Contacts(ContactID)
);

CREATE TABLE IF NOT EXISTS ConvoLabels (
       LabelID  INTEGER,
       ConvoID INTEGER,

       PRIMARY KEY(LabelID, ConvoID),
       FOREIGN KEY(LabelID) REFERENCES Labels(LabelID),
       FOREIGN KEY(ConvoID) REFERENCES Convos(ConvoID)
);

CREATE TABLE IF NOT EXISTS Msgs (
	MsgID         INTEGER PRIMARY KEY,
	StagingID     INTEGER, -- server staging ID, NULL for drafts
	ModSequence   INTEGER,
	Seed          INTEGER,
	RawHash       TEXT, -- sha256 of original input, NULL for drafts
	ConvoID       INTEGER,
	State         INTEGER, -- mdb.MsgState enum
	ParseError    TEXT,

	MailboxID  INTEGER,
	UID        INTEGER, -- uint32, used by IMAP, only filled out by server
	Flags      STRING,  -- JSON '{"flag": 1}' of IMAP flags
	-- TODO: are Flags a replacement for labels?

	EncodedSize INTEGER,

	-- Date is created by the server with time.Now().Unix(), that is,
	-- seconds since epoch.
	-- For drafts, it is the last time the message was edited.
	Date INTEGER NOT NULL,

	Expunged INTEGER, -- time message was expunged (time.Now().Unix())

	-- TODO: what are we going to do with these fields?
	HdrSubject    TEXT, -- TODO: stop seperating from HdrsAll?
	HdrsAll       TEXT, -- processed so newlines are always '\n'
	PlainText     TEXT, -- capped at 128KB
	HTML          TEXT, -- sanitized, capped at 128KB

	HasUnsubscribe INTEGER, -- HTML contains "<a>.*[Uu]nsubscribe</a>""

	UNIQUE (StagingID), -- may be NULL
	FOREIGN KEY(ConvoID) REFERENCES Convos(ConvoID),
	FOREIGN KEY(MailboxID) REFERENCES Mailboxes(MailboxID)
);

CREATE TABLE IF NOT EXISTS MsgAddresses (
	MsgID     INTEGER NOT NULL,
	AddressID INTEGER NOT NULL,
	Role      INTEGER NOT NULL, -- mdb.ContactRole (From:, To:, CC:, BCC:, etc)

	PRIMARY KEY(MsgID, AddressID, Role),
	FOREIGN KEY(MsgID) REFERENCES Msgs(MsgID),
	FOREIGN KEY(AddressID) REFERENCES Addresses(AddressID)
);

-- TODO: move this to its own database.
CREATE TABLE IF NOT EXISTS MsgPartContents (
	BlobID  INTEGER PRIMARY KEY,
	Content BLOB
);

-- MsgParts contains the mime components
CREATE TABLE IF NOT EXISTS MsgParts (
	MsgID         INTEGER NOT NULL,
	PartNum       INTEGER NOT NULL,
	Name          TEXT NOT NULL,
	IsBody        BOOLEAN NOT NULL, -- text or html body of the email
	IsAttachment  BOOLEAN NOT NULL,
	-- TODO IsSent        BOOLEAN NOT NULL, -- has part been uploaded to server
	IsCompressed  BOOLEAN, -- content is gzip compressed
	CompressedSize INTEGER,
	ContentType   TEXT,
	ContentID     TEXT, -- mime header Content-ID
	BlobID        INTEGER,

	Path                    TEXT, -- MIME part path as used in IMAP
	ContentTransferEncoding TEXT,
	ContentTransferSize     INTEGER,
	ContentTransferLines    INTEGER,

	PRIMARY KEY(MsgID, PartNum),
	FOREIGN KEY(MsgID) REFERENCES Msgs(MsgID),
	FOREIGN KEY(BlobID) REFERENCES MsgPartContents(BlobID)
);

CREATE VIRTUAL TABLE IF NOT EXISTS MsgSearch USING fts5(
	MsgID    UNINDEXED,
	ConvoID  UNINDEXED,
	Labels,             -- tokens are "l%x", LabelID.String
	Name,
	Subject,
	Body
	-- TODO prefix "2 3" ?
);

-- TODO remove
INSERT OR IGNORE INTO Contacts (ContactID, Hidden, Robot) VALUES (1, FALSE, FALSE);
INSERT OR IGNORE INTO Labels (LabelID, Label) VALUES (1, 'Personal Mail');
INSERT OR IGNORE INTO Labels (LabelID, Label) VALUES (2, 'Subscriptions');
INSERT OR IGNORE INTO Labels (LabelID, Label) VALUES (3, 'Spam and Trash');

CREATE TRIGGER IF NOT EXISTS MailboxRenameUIDValidity
AFTER UPDATE OF Name ON Mailboxes
FOR EACH ROW
BEGIN
	UPDATE Mailboxes
		SET UIDValidity = (SELECT max(UIDValidity) FROM Mailboxes) + 1
		WHERE MailboxID = new.MailboxID;
END;
`
