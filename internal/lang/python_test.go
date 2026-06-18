package lang

import (
	"testing"

	"github.com/muhammetsafak/archlint/internal/config"
)

func TestPy_Imports(t *testing.T) {
	src := []byte(`import os
import app.db.repo
import app.util as u
import a, b.c
from . import sibling
from .mod import thing
from ..domain.order import Order
from app.services import svc
from __future__ import annotations
    import indented.module
`)
	imps, err := pyLang{}.Imports("x.py", src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, im := range imps {
		got[im.Raw] = im.Line
	}
	for _, want := range []string{
		"os", "app.db.repo", "app.util", "a", "b.c",
		".", ".mod", "..domain.order", "app.services", "__future__", "indented.module",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing module %q (got %v)", want, got)
		}
	}
	if got["os"] != 1 {
		t.Errorf("line of `import os` = %d, want 1", got["os"])
	}
}

func TestPy_Resolve(t *testing.T) {
	cfg := &config.Config{Aliases: map[string]string{"app/": "src/app/"}}
	cases := []struct {
		raw, fileRel, want string
	}{
		{"..db.repo", "app/domain/bad.py", "app/db/repo"},   // up one package, into db
		{".helper", "app/domain/x.py", "app/domain/helper"}, // same package
		{".", "app/domain/x.py", "app/domain"},              // the current package
		{"app.db.repo", "anywhere.py", "src/app/db/repo"},   // absolute + src-layout alias
		{"os", "x.py", "os"},                                // stdlib → matches no layer later
		{"..", "app/x.py", ""},                              // escapes the repo root
	}
	for _, c := range cases {
		if got := (pyLang{}).Resolve(c.raw, c.fileRel, cfg); got != c.want {
			t.Errorf("Resolve(%q from %q) = %q, want %q", c.raw, c.fileRel, got, c.want)
		}
	}
}
