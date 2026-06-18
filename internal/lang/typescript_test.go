package lang

import (
	"testing"

	"github.com/muhammetsafak/archlint/internal/config"
)

func TestTS_Imports(t *testing.T) {
	src := []byte(`
import foo from './a';
import { b, c } from "../b/c";
import * as d from '@/d';
import type { E } from './e';
export { f } from './f';
export * from '../g';
import './side-effect';
const h = require('./h');
const i = await import('./i');
import npm from 'react';
`)
	imps, err := tsLang{}.Imports("x.ts", src)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, im := range imps {
		got[im.Raw] = im.Line
	}
	for _, want := range []string{"./a", "../b/c", "@/d", "./e", "./f", "../g", "./side-effect", "./h", "./i", "react"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing import %q (got %v)", want, got)
		}
	}
	if got["./a"] != 2 {
		t.Errorf("line of './a' = %d, want 2", got["./a"])
	}
}

func TestTS_Resolve(t *testing.T) {
	cfg := &config.Config{Aliases: map[string]string{"@/": "src/"}}
	cases := []struct {
		raw, fileRel, want string
	}{
		{"./order", "src/domain/bad.ts", "src/domain/order"},
		{"../db/repo", "src/domain/bad.ts", "src/db/repo"},
		{"@/db/repo", "src/domain/bad.ts", "src/db/repo"},
		{"react", "src/domain/bad.ts", ""},      // bare npm package
		{"@scope/pkg", "src/domain/bad.ts", ""}, // scoped npm, not the @/ alias
		{"../../outside", "src/x.ts", ""},       // escapes the repo root
	}
	for _, c := range cases {
		if got := (tsLang{}).Resolve(c.raw, c.fileRel, cfg); got != c.want {
			t.Errorf("Resolve(%q from %q) = %q, want %q", c.raw, c.fileRel, got, c.want)
		}
	}
}
