package cofu

import (
	"github.com/cjoudrey/gluahttp"
	"github.com/kohkimakimoto/cofu/infra/backend"
	"github.com/kohkimakimoto/gluaenv"
	"github.com/kohkimakimoto/gluafs"
	"github.com/kohkimakimoto/gluaquestion"
	"github.com/kohkimakimoto/gluatemplate"
	"github.com/kohkimakimoto/gluayaml"
	"github.com/kohkimakimoto/loglv"
	gluajson "layeh.com/gopher-json"
	"github.com/yuin/gluare"
	"github.com/yuin/gopher-lua"
	"log"
	"net/http"
	"path/filepath"
	"strings"
)

func openLibs(app *App) {
	L := app.LState

	loadLResourceClass(L)
	loadLCommandResultClass(L)

	// load core module and resources
	L.PreloadModule("cofu", cofuLuaModuleLoader(app))

	for _, resourceType := range app.ResourceTypes {
		L.SetGlobal(resourceType.Name, L.NewFunction(resourceType.LGFunction()))
	}
	L.SetGlobal("run_command", L.NewFunction(fnRunCommand))
	L.SetGlobal("include_recipe", L.NewFunction(fnIncludeRecipe))
	L.SetGlobal("define", L.NewFunction(fnDefine))

	// buit-in packages
	L.PreloadModule("json", gluajson.Loader)
	L.PreloadModule("fs", gluafs.Loader)
	L.PreloadModule("yaml", gluayaml.Loader)
	L.PreloadModule("template", gluatemplate.Loader)
	L.PreloadModule("question", gluaquestion.Loader)
	L.PreloadModule("env", gluaenv.Loader)
	L.PreloadModule("http", gluahttp.NewHttpModule(&http.Client{}).Loader)
	L.PreloadModule("re", gluare.Loader)
}

func cofuLuaModuleLoader(app *App) func(*lua.LState) int {
	return func(L *lua.LState) int {
		tb := L.NewTable()
		for _, resourceType := range app.ResourceTypes {
			tb.RawSetString(resourceType.Name, L.NewFunction(resourceType.LGFunction()))
		}

		L.SetFuncs(tb, map[string]lua.LGFunction{
			"run_command":    fnRunCommand,
			"include_recipe": fnIncludeRecipe,
			"define":         fnDefine,
		})

		mt := L.NewTable()
		L.SetField(mt, "__index", L.NewFunction(cofuIndex))
		// L.SetField(mt, "__newindex", L.NewFunction(cofuNewIndex))
		L.SetMetatable(tb, mt)

		L.Push(tb)

		return 1
	}
}

func cofuIndex(L *lua.LState) int {
	// cofuModule := L.CheckTable(1)
	index := L.CheckString(2)
	app := GetApp(L)

	var v lua.LValue
	switch index {
	case "os_family":
		v = lua.LString(app.Infra.Command().OSFamily())
	case "os_release":
		v = lua.LString(app.Infra.Command().OSRelease())
	case "os_info":
		v = lua.LString(app.Infra.Command().OSInfo())
	default:
		v = lua.LNil
	}

	L.Push(v)
	return 1
}

const lCommandResultClass = "CommandResult*"

func loadLCommandResultClass(L *lua.LState) {
	mt := L.NewTypeMetatable(lCommandResultClass)
	mt.RawSetString("__index", mt)
	L.SetFuncs(mt, map[string]lua.LGFunction{
		"exit_status": commandResultExitStatus,
		"success":     commandResultSuccess,
		"failure":     commandResultFailure,
		"stdout":      commandResultStdout,
		"stderr":      commandResultStderr,
		"combined":    commandResultCombined,
	})
}

func newLCommandResult(L *lua.LState, result *backend.CommandResult) *lua.LUserData {
	ud := L.NewUserData()
	ud.Value = result
	L.SetMetatable(ud, L.GetTypeMetatable(lCommandResultClass))

	return ud
}

func checkCommandResult(L *lua.LState) *backend.CommandResult {
	ud := L.CheckUserData(1)
	if result, ok := ud.Value.(*backend.CommandResult); ok {
		return result
	}
	L.ArgError(1, "CommandResult expected")

	return nil
}

func commandResultExitStatus(L *lua.LState) int {
	commandResult := checkCommandResult(L)

	L.Push(lua.LNumber(commandResult.ExitStatus))
	return 1
}

func commandResultSuccess(L *lua.LState) int {
	commandResult := checkCommandResult(L)

	L.Push(lua.LBool(commandResult.Success()))
	return 1
}

func commandResultFailure(L *lua.LState) int {
	commandResult := checkCommandResult(L)

	L.Push(lua.LBool(commandResult.Failure()))
	return 1
}

func commandResultStdout(L *lua.LState) int {
	commandResult := checkCommandResult(L)

	L.Push(lua.LString(commandResult.Stdout.String()))
	return 1
}

func commandResultStderr(L *lua.LState) int {
	commandResult := checkCommandResult(L)

	L.Push(lua.LString(commandResult.Stderr.String()))
	return 1
}

func commandResultCombined(L *lua.LState) int {
	commandResult := checkCommandResult(L)

	L.Push(lua.LString(commandResult.Combined.String()))
	return 1
}

func fnRunCommand(L *lua.LState) int {
	// TODO
	command := L.CheckString(1)

	if loglv.IsDebug() {
		log.Printf("    (Debug) command: %s", command)
	}

	app := GetApp(L)
	i := app.Infra
	result := i.RunCommand(command)

	L.Push(newLCommandResult(L, result))
	return 1
}

func fnIncludeRecipe(L *lua.LState) int {
	path := L.CheckString(1)

	if !filepath.IsAbs(path) {
		current := currentDir(L)
		path = filepath.Join(current, path)
	}

	if !strings.HasSuffix(path, ".lua") {
		path += ".lua"
	}

	if err := L.DoFile(path); err != nil {
		panic(err)
	}

	return 0
}

func fnDefine(L *lua.LState) int {
	name := L.CheckString(1)

	// procedural style
	if L.GetTop() == 2 {
		tb := L.CheckTable(2)
		registerDefinition(L, name, tb)
		return 0
	}

	// DSL style
	L.Push(L.NewFunction(func(L *lua.LState) int {
		tb := L.CheckTable(1)
		registerDefinition(L, name, tb)
		return 0
	}))

	return 1
}

func registerDefinition(L *lua.LState, name string, config *lua.LTable) {
	app := GetApp(L)

	// get a last element
	last := config.Remove(-1)
	fn, ok := last.(*lua.LFunction)
	if !ok {
		panic("define's config must have function at the last element.")
	}

	definition := &Definition{
		Name:   name,
		Params: config,
		Func:   fn,
	}

	app.LoadDefinition(definition)
}
