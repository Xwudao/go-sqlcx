package linker

import (
	"testing"

	"github.com/Xwudao/go-sqlcx/internal/config"
)

func TestNamer(t *testing.T) {
	n := newNamer(config.GoOptions{Rename: map[string]string{"is_admin": "Administrator"}})
	cases := map[string]string{"user_id": "UserID", "is_admin": "Administrator", "2fa_code": "_2faCode", "api-key": "ApiKey", "id": "ID"}
	for in, want := range cases {
		if got := n.structName(in); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
	if got := n.tableStruct("public", "categories", "public"); got != "Category" {
		t.Error(got)
	}
	if got := n.tableStruct("audit", "users", "public"); got != "AuditUser" {
		t.Error(got)
	}
	if got := newNamer(config.GoOptions{EmitExactTableNames: true}).tableStruct("public", "users", "public"); got != "Users" {
		t.Error(got)
	}
	if got := snake("ListUserOrderTotals"); got != "list_user_order_totals" {
		t.Error(got)
	}
	if got := snake("GetHTTPLog"); got != "get_http_log" {
		t.Error(got)
	}
}
