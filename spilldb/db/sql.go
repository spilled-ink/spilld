package db

const createSQL = `
PRAGMA auto_vacuum = INCREMENTAL;

-- ServerConfig is a one-row table containing global spilld configuration.
CREATE TABLE IF NOT EXISTS ServerConfig (
	NexusToken TEXT
	-- TODO: consider replicating flags here and using github.com/peterbourgon/ff
);

CREATE TABLE IF NOT EXISTS Users (
	UserID        INTEGER PRIMARY KEY,
	PassHash      TEXT NOT NULL,    -- bcrypt of used password
	SecretBoxKey  TEXT NOT NULL,    -- hex encoded 32-byte key
	FullName      TEXT NOT NULL,
	PhoneNumber   TEXT NOT NULL,
	PhoneVerified BOOLEAN NOT NULL,
	Admin         BOOLEAN NOT NULL,
	Locked        BOOLEAN NOT NULL
);

CREATE TABLE IF NOT EXISTS UserAddresses (
	Address     TEXT PRIMARY KEY, -- "user@domain", always lower case
	UserID      INTEGER NOT NULL,
	PrimaryAddr BOOLEAN,

	FOREIGN KEY(UserID) REFERENCES Users(UserID)
);

CREATE TABLE IF NOT EXISTS Devices (
	DeviceID        INTEGER PRIMARY KEY,
	UserID          INTEGER NOT NULL,
	DeviceName      TEXT NOT NULL,
	AppPassHash     TEXT,
	Deleted         BOOLEAN,
	Created         INTEGER NOT NULL, -- time.Unix
	LastAccessTime  INTEGER, -- time.Unix
	LastAccessAddr  TEXT,

	FOREIGN KEY(UserID) REFERENCES Users(UserID)
);

CREATE TABLE IF NOT EXISTS Msgs (
	StagingID     INTEGER PRIMARY KEY,
	Sender        TEXT NOT NULL,
	DKIM          TEXT,             -- "PASS" for valid signatures
	DateReceived  INTEGER NOT NULL, -- time.Now.Unix() from the server
	ReadyDate     INTEGER,          -- UnixNano() at moment of DeliveryToProcess -> DeliveryReceived
	UserID        INTEGER,          -- set by createmsg on output messages

	FOREIGN KEY(UserID) REFERENCES Users(UserID)
);

-- MsgRecipients acts as the "envelope" of a Msg.
CREATE TABLE IF NOT EXISTS MsgRecipients (
	StagingID     INTEGER NOT NULL,
	Recipient     TEXT NOT NULL,    -- bob@example.com, unique when sending
	FullAddress   TEXT NOT NULL,    -- Bob Doe <bob@example.com>
	DeliveryState INTEGER NOT NULL, -- DeliveryState Go type

	PRIMARY KEY(StagingID, Recipient),
	FOREIGN KEY(StagingID) REFERENCES Msgs(StagingID),
	FOREIGN KEY(Recipient) REFERENCES UserAddresses(Address)
);

-- MsgRaw holds the fully-encoded raw contents of a message.
-- It remains entirely unmodified from how it was received.
CREATE TABLE IF NOT EXISTS MsgRaw (
	StagingID INTEGER PRIMARY KEY,
	Content   BLOB,

	FOREIGN KEY(StagingID) REFERENCES Msgs(StagingID)
);

-- MsgFull holds the fully-encoded raw contents of a message.
-- It has been processed and is ready for delivery.
CREATE TABLE IF NOT EXISTS MsgFull (
	StagingID INTEGER PRIMARY KEY,
	Content   BLOB,

	FOREIGN KEY(StagingID) REFERENCES Msgs(StagingID)
);

-- Deliveries contains a record for each email delivery attempt made.
-- On successful delivery, Code == 250 and the DeliveryState in MsgRecipients changes.
-- There are many possible codes, a core sample are on https://cr.yp.to/smtp/mail.html.
CREATE TABLE IF NOT EXISTS Deliveries (
	AttemptID INTEGER PRIMARY KEY,
	StagingID INTEGER NOT NULL,
	Recipient TEXT NOT NULL,
	Code      INTEGER NOT NULL,
	Date      INTEGER NOT NULL, -- time.Now().Unix()
	Details   TEXT,

	FOREIGN KEY(StagingID, Recipient) REFERENCES MsgRecipients(StagingID, Recipient)
);
`
