package meta

import (
	"slices"
	"strings"
	"testing"
)

func TestItemColumnsAreThoseOfTheTable(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	rows, err := s.r.Query(`SELECT name FROM pragma_table_info('items')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var table []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		table = append(table, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	listed := strings.Split(itemCols, ", ")
	slices.Sort(table)
	slices.Sort(listed)
	if !slices.Equal(table, listed) {
		t.Fatalf("the table of items has the columns\n%q\nand the code lists\n%q", table, listed)
	}
}

func TestColumnsOfDriveAreListed(t *testing.T) {
	t.Parallel()
	want := "mime, md5, version, r_exists, r_md5, r_version, r_name, r_parent, r_trashed, r_mtime, " +
		"denied, create_tried, rev"
	if driveCols != want {
		t.Fatalf("the columns of Drive are\n%s\nwant\n%s", driveCols, want)
	}
}
