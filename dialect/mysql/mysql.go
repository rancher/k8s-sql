package mysql

import (
	_ "github.com/go-sql-driver/mysql"
	"github.com/rancher/k8s-sql"
	"github.com/rancher/k8s-sql/dialect"
)

func init() {
	rdbms.Register("mysql", NewMySQL())
}

func NewMySQL() *dialect.Generic {
	return &dialect.Generic{
		CleanupSQL:      "delete from key_value where ttl > 0 and ttl < ?",
		GetSQL:          "select name, value, revision from key_value where name = ?",
		ListSQL:         "select name, value, revision from key_value where name like ?",
		ListRevisionSQL: "select name, value, revision from key_value where revision > ? and name like ?",
		CreateSQL:       "insert into key_value(name, value, revision, ttl) values(?, ?, ?, ?)",
		DeleteSQL:       "delete from key_value where name = ? and revision = ?",
		UpdateSQL:       "update key_value set value = ?, revision = ? where name = ? and revision = ?",
		GetRevisionSQL:  "select max(revision) from key_value",
	}
}
