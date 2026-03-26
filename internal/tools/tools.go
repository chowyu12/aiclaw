package tools

import (
	"context"

	"github.com/chowyu12/aiclaw/internal/tools/browser"
	"github.com/chowyu12/aiclaw/internal/tools/builtin"
	"github.com/chowyu12/aiclaw/internal/tools/canvas"
	"github.com/chowyu12/aiclaw/internal/tools/codeinterp"
	"github.com/chowyu12/aiclaw/internal/tools/crontab"
	"github.com/chowyu12/aiclaw/internal/tools/editfile"
	"github.com/chowyu12/aiclaw/internal/tools/findfile"
	"github.com/chowyu12/aiclaw/internal/tools/grepfile"
	"github.com/chowyu12/aiclaw/internal/tools/ls"
	"github.com/chowyu12/aiclaw/internal/tools/process"
	"github.com/chowyu12/aiclaw/internal/tools/readfile"
	"github.com/chowyu12/aiclaw/internal/tools/result"
	"github.com/chowyu12/aiclaw/internal/tools/shellexec"
	"github.com/chowyu12/aiclaw/internal/tools/urlreader"
	"github.com/chowyu12/aiclaw/internal/tools/writefile"
)

type FileResult = result.FileResult

var ParseFileResult = result.ParseFileResult

func DefaultBuiltins() map[string]func(context.Context, string) (string, error) {
	m := builtin.Handlers()
	m["read"] = readfile.Handler
	m["write"] = writefile.Handler
	m["edit"] = editfile.Handler
	m["grep"] = grepfile.Handler
	m["find"] = findfile.Handler
	m["ls"] = ls.Handler
	m["exec"] = shellexec.Handler
	m["process"] = process.Handler
	m["web_fetch"] = urlreader.Handler
	m["browser"] = browser.Handler
	m["canvas"] = canvas.Handler
	m["cron"] = crontab.Handler
	m["code_interpreter"] = codeinterp.Handler
	return m
}
