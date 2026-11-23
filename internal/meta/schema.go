package meta

const schemaVersion = 25

const schemaV24 = 24

const schemaV23 = 23

const schemaV22 = 22

const schemaV21 = 21

const schemaV20 = 20

const schemaV19 = 19

const schemaV18 = 18

const schemaV17 = 17

const schemaV16 = 16

const schemaV15 = 15

const schemaV14 = 14

const schemaV13 = 13

const schemaV12 = 12

const schemaV7 = 7

const schemaV8 = 8

const schemaV9 = 9

const schemaV11 = 11

const schemaV10 = 10

const schema = `
CREATE TABLE IF NOT EXISTS state (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
) WITHOUT ROWID;

-- The journal of the changes of a file's fileId that are not finished, on
-- its way into Lost and Found or from a key of its own to Drive: the fileId
-- the file had, by the one it took.
CREATE TABLE IF NOT EXISTS shelf (
	took TEXT PRIMARY KEY,
	had  TEXT NOT NULL
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS items (
	ino     INTEGER PRIMARY KEY AUTOINCREMENT,
	id      TEXT NOT NULL UNIQUE,
	kind    INTEGER NOT NULL,
	mime    TEXT NOT NULL,
	name    TEXT NOT NULL,
	parent  TEXT NOT NULL,
	dname   TEXT NOT NULL,
	trashed INTEGER NOT NULL,
	size    INTEGER NOT NULL,
	md5     TEXT NOT NULL,
	version INTEGER NOT NULL,
	mtime   INTEGER NOT NULL,
	ctime   INTEGER NOT NULL,
	r_exists     INTEGER NOT NULL DEFAULT 1,
	dirty        INTEGER NOT NULL DEFAULT 0,
	r_md5        TEXT NOT NULL DEFAULT '',
	r_version    INTEGER NOT NULL DEFAULT 0,
	content_gen  INTEGER NOT NULL DEFAULT 0,
	meta_dirty   INTEGER NOT NULL DEFAULT 0,
	r_name       TEXT NOT NULL DEFAULT '',
	r_parent     TEXT NOT NULL DEFAULT '',
	r_trashed    INTEGER NOT NULL DEFAULT 0,
	r_mtime      INTEGER NOT NULL DEFAULT 0,
	target       TEXT NOT NULL DEFAULT '',
	upload_after INTEGER NOT NULL DEFAULT 0,
	denied       INTEGER NOT NULL DEFAULT 0,
	base         INTEGER NOT NULL DEFAULT 0,
	base_tail    INTEGER NOT NULL DEFAULT 0,
	create_tried INTEGER NOT NULL DEFAULT 0,
	rev          TEXT NOT NULL DEFAULT '',
	lost         INTEGER NOT NULL DEFAULT 0,
	service      INTEGER NOT NULL DEFAULT 0,
	note_for     INTEGER NOT NULL DEFAULT 0,
	shown_as     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS id_pool (
	id TEXT PRIMARY KEY
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS items_children ON items(parent, dname, ctime, id);

CREATE INDEX IF NOT EXISTS items_notes ON items(note_for) WHERE note_for != 0;

CREATE INDEX IF NOT EXISTS items_shown ON items(parent, shown_as);

CREATE INDEX IF NOT EXISTS items_unnamed ON items(ino) WHERE shown_as = '' AND trashed = 0;

DROP TRIGGER IF EXISTS items_renamed;
CREATE TRIGGER items_renamed
AFTER UPDATE OF dname, parent, trashed, kind ON items
WHEN NEW.shown_as != ''
	AND (NEW.dname != OLD.dname OR NEW.parent != OLD.parent OR NEW.trashed != OLD.trashed OR NEW.kind != OLD.kind)
BEGIN
	UPDATE items SET shown_as = '' WHERE ino = NEW.ino;
END;

CREATE INDEX IF NOT EXISTS items_target ON items(target) WHERE target != '';

CREATE INDEX IF NOT EXISTS items_dirty ON items(id) WHERE dirty = 1;

CREATE INDEX IF NOT EXISTS items_dirs ON items(parent) WHERE kind = 1;

CREATE TABLE IF NOT EXISTS pieces (
	ino   INTEGER NOT NULL,
	off   INTEGER NOT NULL,
	len   INTEGER NOT NULL,
	PRIMARY KEY (ino, off)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS uploaded (
	at    INTEGER NOT NULL,
	bytes INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS usage (
	minute INTEGER PRIMARY KEY,
	units  INTEGER NOT NULL,
	bytes  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS ops (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	ino        INTEGER NOT NULL,
	state      INTEGER NOT NULL,
	refused    INTEGER NOT NULL DEFAULT 0,
	refused_for TEXT NOT NULL DEFAULT '',
	busy       INTEGER NOT NULL DEFAULT 0,
	busy_for   TEXT NOT NULL DEFAULT '',
	net        INTEGER NOT NULL DEFAULT 0,
	noted      INTEGER NOT NULL DEFAULT 0,
	attempts   INTEGER NOT NULL DEFAULT 0,
	next_try   INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS ops_ino ON ops(ino, id);

CREATE TABLE IF NOT EXISTS op_deps (
	op    INTEGER NOT NULL,
	needs INTEGER NOT NULL,
	PRIMARY KEY (op, needs)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS op_deps_needs ON op_deps(needs);

-- The triggers below note in touched each item changed here that a
-- statement changes in what goes to Drive: commit brings the operations of
-- the noted items in line with them.
-- An item that only follows Drive is not noted, and neither is the time of
-- a file whose content waits for Drive anyway.
-- A trigger takes the conflict rule of the statement that fires it, so the
-- note is put only where there is none.
-- The triggers are made anew at every start, so that they are those of the
-- build that runs.
CREATE TABLE IF NOT EXISTS touched (
	ino INTEGER PRIMARY KEY
) WITHOUT ROWID;

DROP TRIGGER IF EXISTS items_changed;
CREATE TRIGGER items_changed
AFTER UPDATE OF name, parent, trashed, mtime, dirty, meta_dirty, lost, service ON items
WHEN (NEW.name != OLD.name OR NEW.parent != OLD.parent OR NEW.trashed != OLD.trashed OR NEW.dirty != OLD.dirty
		OR NEW.meta_dirty != OLD.meta_dirty OR NEW.lost != OLD.lost OR NEW.service != OLD.service
		OR (NEW.mtime != OLD.mtime AND NEW.dirty = 0))
	AND (NEW.dirty OR NEW.meta_dirty OR NOT NEW.r_exists OR OLD.dirty OR OLD.meta_dirty OR NOT OLD.r_exists
		OR NEW.lost != OLD.lost OR NEW.service != OLD.service)
BEGIN
	INSERT INTO touched(ino) SELECT NEW.ino WHERE NOT EXISTS (SELECT 1 FROM touched WHERE ino = NEW.ino);
END;

DROP TRIGGER IF EXISTS items_made;
CREATE TRIGGER items_made
AFTER INSERT ON items
WHEN NEW.dirty OR NEW.meta_dirty OR NOT NEW.r_exists
BEGIN
	INSERT INTO touched(ino) SELECT NEW.ino WHERE NOT EXISTS (SELECT 1 FROM touched WHERE ino = NEW.ino);
END;

DROP TRIGGER IF EXISTS items_gone;
CREATE TRIGGER items_gone
AFTER DELETE ON items
WHEN EXISTS (SELECT 1 FROM ops WHERE ino = OLD.ino)
BEGIN
	INSERT INTO touched(ino) SELECT OLD.ino WHERE NOT EXISTS (SELECT 1 FROM touched WHERE ino = OLD.ino);
END;

CREATE TABLE IF NOT EXISTS upload_sessions (
	ino     INTEGER PRIMARY KEY,
	address TEXT NOT NULL,
	name    TEXT NOT NULL,
	parent  TEXT NOT NULL,
	mtime   INTEGER NOT NULL
);

-- An upload session lives no longer than the change of the file it was
-- opened for: a file that is no longer changed here, or no longer here,
-- takes its session along, and a later change opens one of its own.
DROP TRIGGER IF EXISTS upload_sessions_cleaned;
CREATE TRIGGER upload_sessions_cleaned
AFTER UPDATE OF dirty ON items
WHEN OLD.dirty = 1 AND NEW.dirty = 0
BEGIN
	DELETE FROM upload_sessions WHERE ino = NEW.ino;
END;

DROP TRIGGER IF EXISTS upload_sessions_gone;
CREATE TRIGGER upload_sessions_gone
AFTER DELETE ON items
BEGIN
	DELETE FROM upload_sessions WHERE ino = OLD.ino;
END;

DELETE FROM upload_sessions WHERE ino NOT IN (SELECT ino FROM items WHERE dirty = 1);
`

const schemaV6 = 6
