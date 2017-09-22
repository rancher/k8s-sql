package dialect

import (
	"github.com/rancher/k8s-sql"
)

func init() {
	rdbms.Register("mysql", newMySQL())
}

func newMySQL() *generic {
	return &generic{
		cleanup: "delete from key_value where ttl > 0 and ttl < ?",
		get:     "select name, value, revision from key_value where name = ?",
		list:    "select name, value, revision from key_value where name like ?",
		create:  "insert into key_value(name, value, revision, ttl) values(?, ?, 1, ?)",
		delete:  "delete from key_value where name = ? and revision = ?",
		update:  "update key_value set value = ?, revision = ? where name = ? and revision = ?",
	}
}
