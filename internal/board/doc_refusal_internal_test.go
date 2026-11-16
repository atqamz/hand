package board

import (
	"fmt"
	"io/fs"
	"net/http"
	"syscall"
	"testing"
)

func TestDocRefusalMapsEveryErrorTheViewerOpens(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		msg    string
	}{
		{fs.ErrNotExist, http.StatusNotFound, "there is no file or folder at this path"},
		{syscall.ENOTDIR, http.StatusNotFound, "there is no file or folder at this path"},
		{fmt.Errorf("open: %w", fs.ErrNotExist), http.StatusNotFound, "there is no file or folder at this path"},
		{errChanged, http.StatusConflict, errChanged.Error()},
		{errHidden, http.StatusForbidden, errHidden.Error()},
		{errOutside, http.StatusForbidden, errOutside.Error()},
		{errKind, http.StatusForbidden, errKind.Error()},
		{fs.ErrPermission, http.StatusForbidden, "the Board cannot read this path"},
	} {
		if status, msg := docRefusal(c.err); status != c.status || msg != c.msg {
			t.Errorf("docRefusal(%v) = %d %q, want %d %q", c.err, status, msg, c.status, c.msg)
		}
		if got := apiCode(c.status); got == "internal" {
			t.Errorf("%d has no API code", c.status)
		}
	}
}
