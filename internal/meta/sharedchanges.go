package meta

import (
	"fmt"
	"strconv"
	"strings"
)

const stateSharedChanges = "shared_changes"

func sharedRow(r string) string {
	return r + ".shared = 1 OR " + r + ".parent = '" + SharedWithMeID + "'"
}

func placeRow(r string) string {
	return r + ".kind = " + strconv.Itoa(int(KindDir)) + ` OR EXISTS (SELECT 1 FROM items s
		WHERE s.target = ` + r + `.id AND s.target != '' AND s.shared = 1)`
}

var sharedTriggers = func() string {
	listed := strings.Fields("id kind mime name parent trashed shared ctime target")
	placed := strings.Fields("id parent kind trashed")
	count := `UPDATE state SET value = CAST(value AS INTEGER) + 1 WHERE key = '` + stateSharedChanges + `';`
	return fmt.Sprintf(`
BEGIN;
INSERT OR IGNORE INTO state(key, value) VALUES ('%[1]s', '0');

DROP TRIGGER IF EXISTS shared_changed;
CREATE TRIGGER shared_changed
AFTER UPDATE OF %[9]s ON items
WHEN (%[2]s OR %[3]s) AND (%[7]s) OR (%[4]s OR %[5]s) AND (%[8]s)
BEGIN %[6]s END;

DROP TRIGGER IF EXISTS shared_made;
CREATE TRIGGER shared_made
AFTER INSERT ON items
WHEN %[3]s OR %[5]s
BEGIN %[6]s END;

DROP TRIGGER IF EXISTS shared_gone;
CREATE TRIGGER shared_gone
AFTER DELETE ON items
WHEN %[2]s OR %[4]s
BEGIN %[6]s END;
COMMIT;
`, stateSharedChanges, sharedRow("OLD"), sharedRow("NEW"), placeRow("OLD"), placeRow("NEW"), count,
		changed(listed...), changed(placed...), strings.Join(listed, ", "))
}()

func changed(cols ...string) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = "NEW." + c + " != OLD." + c
	}
	return strings.Join(out, " OR ")
}

func (s *Store) countSharedChanges() error {
	if _, err := s.w.Exec(sharedTriggers); err != nil {
		return fmt.Errorf("meta: count the changes of Shared with me: %w", err)
	}
	return nil
}

func sharedChanges(tx txn) (int64, error) {
	var n int64
	err := tx.QueryRow(`SELECT coalesce((SELECT CAST(value AS INTEGER) FROM state WHERE key = ?), -1)`,
		stateSharedChanges).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("meta: the changes of Shared with me: %w", err)
	}
	return n, nil
}
