package knobs

import (
	"slices"
	"sort"
	"strings"
)

// Engine resolves one application's configuration: attached schemas, then layers in
// precedence order, lowest first. Attach and Add are closed by the first Resolve.
type Engine struct {
	set    *schemaSet
	layers []Layer
	closed bool
}

// New creates the engine for an application. The name is lowercase snake case: it is the root
// of every file, the prefix of every key and environment variable.
func New(app string) (*Engine, error) {
	if !namePattern.MatchString(app) {
		return nil, newError(CodeInvalidArgument, "args", "", "the application name is lowercase snake case")
	}
	return &Engine{set: &schemaSet{app: app, modules: map[string]*module{}}}, nil
}

// Module is everything one Go module gives the engine about its own knobs, so main only attaches it:
// its JSON Schema, JSON or YAML, in any draft the validator supports (2020-12 when it names none);
// its code values (the code layer, under every other); and its own files, which apply right after the
// layer of the same name (LayerGlobal, LayerUser); either path may be empty or missing.
//
// A schema with x-knobs-module is one module: its code values are keyed by the module's knob names
// (port, http.port) and its files hold the module's mapping directly. A schema without it is an
// application schema, each of whose properties is a module: its code values are keyed by
// <module>.<name> and its files hold the mapping of modules directly, with no application root.
type Module struct {
	Schema               []byte
	Code                 map[string]any
	GlobalPath, UserPath string
}

// Attach mounts each module at <app>.<module>. Two modules with one name fail with module_conflict.
func (e *Engine) Attach(modules ...Module) error {
	if e.closed {
		return newError(CodeInvalidArgument, "schema", "", "Attach after Resolve")
	}
	for _, mod := range modules {
		if err := e.attach(mod); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) attach(mod Module) error {
	mods, single, err := parseSchema(e.set.app, mod.Schema)
	if err != nil {
		return err
	}
	src := &source{single: single, code: mod.Code, globalPath: mod.GlobalPath, userPath: mod.UserPath}
	for _, m := range mods {
		if _, dup := e.set.modules[m.name]; dup {
			return newError(CodeModuleConflict, "schema", e.set.key(m.name, ""), "two schemas for one module")
		}
		src.modules = append(src.modules, m.name)
	}
	for _, m := range mods {
		m.src = src
		e.set.modules[m.name] = m
	}
	e.set.sources = append(e.set.sources, src)
	return nil
}

// Add appends layers above every layer added before them, in the order given.
func (e *Engine) Add(layers ...Layer) error {
	if e.closed {
		return newError(CodeInvalidArgument, "args", "", "Add after Resolve")
	}
	e.layers = append(e.layers, layers...)
	return nil
}

// Schema is the master schema: the application object with one property per attached module.
func (e *Engine) Schema() map[string]any { return e.set.master() }

// Resolve validates and merges every layer into the process configuration. It closes the
// engine: later Attach and Add calls fail.
func (e *Engine) Resolve() (*Config, error) {
	e.closed = true
	view := &Schemas{set: e.set}
	values := map[string]Value{}
	for _, l := range e.withModuleFiles() {
		settings, err := l.Load(view)
		if err != nil {
			return nil, err
		}
		if err := merge(e.set, l, sortSettings(settings), values); err != nil {
			return nil, err
		}
	}
	if err := checkModules(e.set, values); err != nil {
		return nil, err
	}
	return &Config{set: e.set, values: values}, nil
}

// withModuleFiles is the layer list with each attached Module's own files right after the layer of
// the same name, in the order of each one's first module name.
func (e *Engine) withModuleFiles() []Layer {
	srcs := slices.Clone(e.set.sources)
	sort.Slice(srcs, func(i, j int) bool { return srcs[i].modules[0] < srcs[j].modules[0] })
	var out []Layer
	for _, l := range e.layers {
		out = append(out, l)
		for _, src := range srcs {
			switch {
			case l.Name() == LayerGlobal && src.globalPath != "":
				out = append(out, sourceFile(LayerGlobal, src, src.globalPath))
			case l.Name() == LayerUser && src.userPath != "":
				out = append(out, sourceFile(LayerUser, src, src.userPath))
			}
		}
	}
	return out
}

// merge validates one layer's settings, in key order, and writes them over the values below.
func merge(set *schemaSet, l Layer, settings []Setting, values map[string]Value) error {
	pol := l.Policy()
	for _, st := range settings {
		if st.Err != "" {
			return keyError(set, st.Err, l.Name(), st.Key, "")
		}
		p := set.prop(st.Module, st.Name)
		if p.secret && pol.RejectSecrets != "" {
			return keyError(set, pol.RejectSecrets, l.Name(), st.Key, "a secret is set only in the secrets file, env or runtime")
		}
		v, code := convert(p, st)
		if code != "" {
			return keyError(set, code, l.Name(), st.Key, "")
		}
		values[st.Key] = Value{Key: st.Key, Value: v, Layer: l.Name(), Secret: p.secret || pol.SecretValues}
	}
	return nil
}

// convert turns a setting into a value of its property's type that passes its rules.
func convert(p *property, st Setting) (any, string) {
	var v any
	var code string
	if text, ok := st.Value.(string); ok && st.Text {
		v, code = coerceText(p, text)
	} else {
		v, code = coerceTyped(p, st.Value)
	}
	if code != "" {
		return nil, code
	}
	return v, p.check(v)
}

// keyError is an error for a dotted key, naming the owner of the key's module when its schema gives one.
func keyError(set *schemaSet, code, layer, key, msg string) *Error {
	e := newError(code, layer, key, msg)
	if parts := strings.SplitN(key, ".", 3); len(parts) > 1 {
		e.Owner = ownerRef(set.modules[parts[1]])
	}
	return e
}

// Standard configures AddStandard. Empty paths are resolved from the path knobs
// <app>.knobs.global_path, user_path and secrets_path (cli, then env), else the defaults; a
// secrets file is read only when a path is given.
type Standard struct {
	GlobalPath, UserPath, SecretsPath string
	Env                               func(string) (string, bool)
	Knobs                             []string
	Runtime                           map[string]string
}

// AddStandard adds the standard layers in precedence order: code, schema, global, user, secrets,
// env, cli and, when given, runtime.
func (e *Engine) AddStandard(s Standard) error {
	paths, knobs, err := e.pathKnobs(s.Knobs)
	if err != nil {
		return err
	}
	env := s.Env
	if env == nil {
		env = func(string) (string, bool) { return "", false }
	}
	global, gx := e.pick(s.GlobalPath, "global_path", paths, env, defaultGlobalPath(e.set.app, env))
	user, ux := e.pick(s.UserPath, "user_path", paths, env, DefaultUserPath(e.set.app, env))
	secrets, sx := e.pick(s.SecretsPath, "secrets_path", paths, env, "")
	layers := []Layer{
		Code(nil), Defaults(), File(LayerGlobal, global, gx), File(LayerUser, user, ux), File(LayerSecrets, secrets, sx),
		Env(s.Env), CLI(knobs),
	}
	if s.Runtime != nil {
		layers = append(layers, Runtime(s.Runtime))
	}
	return e.Add(layers...)
}

// pathKnobs splits the <app>.knobs.* arguments from the rest.
func (e *Engine) pathKnobs(args []string) (map[string]string, []string, error) {
	paths := map[string]string{}
	var rest []string
	prefix := e.set.app + "." + reservedModule + "."
	for _, a := range args {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return nil, nil, newError(CodeInvalidArgument, LayerCLI, "", "want KEY=VALUE")
		}
		if name, isPath := strings.CutPrefix(k, prefix); isPath {
			paths[name] = v
			continue
		}
		rest = append(rest, a)
	}
	return paths, rest, nil
}

// pick chooses a file path: the given one, the cli path knob, the env path knob, else the
// default. Every source but the default names the file explicitly.
func (e *Engine) pick(given, knob string, paths map[string]string, env func(string) (string, bool),
	def string) (string, bool) {
	if given != "" {
		return given, true
	}
	if p, ok := paths[knob]; ok {
		return p, true
	}
	if p, ok := env(strings.ToUpper(e.set.app + "_" + reservedModule + "_" + knob)); ok && p != "" {
		return p, true
	}
	return def, false
}
