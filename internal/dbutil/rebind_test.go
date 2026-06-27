package dbutil

import "testing"

func TestRebind(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"none", "SELECT 1", "SELECT 1"},
		{"single", "SELECT * FROM t WHERE id = ?", "SELECT * FROM t WHERE id = $1"},
		{"multi", "INSERT INTO t(a,b,c) VALUES(?,?,?)", "INSERT INTO t(a,b,c) VALUES($1,$2,$3)"},
		{"mixed", "UPDATE t SET a=?, b=? WHERE id=?", "UPDATE t SET a=$1, b=$2 WHERE id=$3"},
		{"in_clause", "DELETE FROM t WHERE id IN (?,?,?)", "DELETE FROM t WHERE id IN ($1,$2,$3)"},
		{
			"on_conflict",
			"INSERT INTO t(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v",
			"INSERT INTO t(k,v) VALUES($1,$2) ON CONFLICT(k) DO UPDATE SET v=excluded.v",
		},
		{
			"question_in_literal_untouched",
			"SELECT * FROM t WHERE label = 'why?' AND id = ?",
			"SELECT * FROM t WHERE label = 'why?' AND id = $1",
		},
		{
			"escaped_quote_in_literal",
			"SELECT * FROM t WHERE note = 'it''s a ? mark' AND id = ?",
			"SELECT * FROM t WHERE note = 'it''s a ? mark' AND id = $1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Rebind(tc.in); got != tc.want {
				t.Errorf("Rebind(%q)\n  got  %q\n  want %q", tc.in, got, tc.want)
			}
		})
	}
}
