package tool

import (
	"context"

	"github.com/chowyu12/aiclaw/internal/tool/browser"
	"github.com/chowyu12/aiclaw/internal/tool/builtin"
	"github.com/chowyu12/aiclaw/internal/tool/canvas"
	"github.com/chowyu12/aiclaw/internal/tool/codeinterp"
	"github.com/chowyu12/aiclaw/internal/tool/crontab"
	"github.com/chowyu12/aiclaw/internal/tool/editfile"
	"github.com/chowyu12/aiclaw/internal/tool/findfile"
	"github.com/chowyu12/aiclaw/internal/tool/grepfile"
	"github.com/chowyu12/aiclaw/internal/tool/ls"
	"github.com/chowyu12/aiclaw/internal/tool/process"
	"github.com/chowyu12/aiclaw/internal/tool/readfile"
	"github.com/chowyu12/aiclaw/internal/tool/result"
	"github.com/chowyu12/aiclaw/internal/tool/shellexec"
	"github.com/chowyu12/aiclaw/internal/tool/urlreader"
	"github.com/chowyu12/aiclaw/internal/tool/writefile"
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
