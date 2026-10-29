package sqlcx_test

import (
	"fmt"
	"sync"
	"testing"

	"github.com/Xwudao/go-sqlcx/internal/testdb/db"
	"github.com/Xwudao/go-sqlcx/internal/testdb/dynamic"
	"github.com/Xwudao/go-sqlcx/sqlcx"
)

// TestConcurrentBuild extends one shared query from many goroutines; run
// with -race. Builders are immutable, so every goroutine must see only its
// own conditions.
func TestConcurrentBuild(t *testing.T) {
	u := dynamic.Users
	base := u.Query().Where(u.Status.Eq(db.UserStatusActive)).OrderBy(u.ID.Asc())
	want, _, _ := base.Build()
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := range 64 {
		wg.Go(func() {
			q := base.Where(u.Age.Gt(int32(i))).Limit(i + 1)
			for range 50 {
				q = q.Where(sqlcx.Or(u.Name.Eq(fmt.Sprint(i)), u.Tags.Has("x")))
			}
			sql, args, err := q.Build()
			if err != nil {
				errs <- err
				return
			}
			if len(args) != 1+1+50*2+1 || args[1] != int32(i) {
				errs <- fmt.Errorf("goroutine %d: unexpected args %v", i, args[:3])
			}
			_ = sql
			_, _, _ = dynamic.Users.As(fmt.Sprintf("u%d", i)).Query().Build()
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if again, _, _ := base.Build(); again != want {
		t.Fatal("shared base query was mutated")
	}
}
