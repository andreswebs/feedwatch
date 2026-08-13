package command

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	cliv3 "github.com/urfave/cli/v3"
)

// The CLI's input surface is a projection of the library's request types rather
// than a parallel declaration of it, so a new request field cannot exist without
// a flag and a renamed one cannot leave a stale flag behind (ADR 0007).
//
// Reflection describes shape only. Five tags carry it, and none of them carries
// semantics: validation stays in each request's Validate method, written in
// ordinary Go, because encoding rules into tags means inventing a validation
// mini-language that grows without bound.
//
//	flag      flag name; "-" excludes the field from the CLI surface entirely
//	arg       the field is a positional argument with this name, not a flag
//	alias     comma-separated flag aliases
//	default   compiled-in default, as the string the flag's parser accepts
//	usage     usage text
//	variadic  "true" on an arg field that consumes the remaining positionals
//
// Every field must carry a flag or an arg tag. A field with neither, and a field
// whose type is absent from the mapping table below, are programming errors that
// cannot fail at compile time, so both panic during command-tree construction:
// every test in the suite builds the tree, which turns the mistake into an
// immediate, unmissable failure rather than a silently missing flag.

// fieldSpec is the shape one struct tag set describes.
type fieldSpec struct {
	name    string
	aliases []string
	usage   string
	def     string
}

// flagKind is one entry of the type mapping: how a Go type becomes a flag and how
// the parsed value is read back into the field. Construction and binding sit in
// one value so the two halves of the mapping cannot drift apart.
type flagKind struct {
	newFlag func(fieldSpec) (cliv3.Flag, error)
	bind    func(cmd *cliv3.Command, name string, dst reflect.Value)
}

// flagKinds is the supported mapping from request field type to CLI flag:
//
//	string         StringFlag        cmd.String
//	bool           BoolFlag          cmd.Bool
//	int            IntFlag           cmd.Int
//	*int           IntFlag           cmd.Int, but only when the flag was given
//	time.Duration  DurationFlag      cmd.Duration
//	[]string       StringSliceFlag   cmd.StringSlice
//
// The pointer entry is the one place where the Go type and the flag type
// deliberately disagree: a plain flag whose absence leaves the pointer nil is
// what keeps "--keep-days 0" distinguishable from no --keep-days at all.
var flagKinds = map[reflect.Type]flagKind{
	reflect.TypeFor[string](): {
		newFlag: func(s fieldSpec) (cliv3.Flag, error) {
			return &cliv3.StringFlag{Name: s.name, Aliases: s.aliases, Usage: s.usage, Value: s.def}, nil
		},
		bind: func(cmd *cliv3.Command, name string, dst reflect.Value) {
			dst.SetString(cmd.String(name))
		},
	},
	reflect.TypeFor[bool](): {
		newFlag: func(s fieldSpec) (cliv3.Flag, error) {
			f := &cliv3.BoolFlag{Name: s.name, Aliases: s.aliases, Usage: s.usage}
			if s.def != "" {
				v, err := strconv.ParseBool(s.def)
				if err != nil {
					return nil, err
				}
				f.Value = v
			}
			return f, nil
		},
		bind: func(cmd *cliv3.Command, name string, dst reflect.Value) {
			dst.SetBool(cmd.Bool(name))
		},
	},
	reflect.TypeFor[int](): {
		newFlag: intFlag,
		bind: func(cmd *cliv3.Command, name string, dst reflect.Value) {
			dst.SetInt(int64(cmd.Int(name)))
		},
	},
	reflect.TypeFor[*int](): {
		newFlag: intFlag,
		bind: func(cmd *cliv3.Command, name string, dst reflect.Value) {
			if !cmd.IsSet(name) {
				return
			}
			v := cmd.Int(name)
			dst.Set(reflect.ValueOf(&v))
		},
	},
	reflect.TypeFor[time.Duration](): {
		newFlag: func(s fieldSpec) (cliv3.Flag, error) {
			f := &cliv3.DurationFlag{Name: s.name, Aliases: s.aliases, Usage: s.usage}
			if s.def != "" {
				v, err := time.ParseDuration(s.def)
				if err != nil {
					return nil, err
				}
				f.Value = v
			}
			return f, nil
		},
		bind: func(cmd *cliv3.Command, name string, dst reflect.Value) {
			dst.SetInt(int64(cmd.Duration(name)))
		},
	},
	reflect.TypeFor[[]string](): {
		newFlag: func(s fieldSpec) (cliv3.Flag, error) {
			f := &cliv3.StringSliceFlag{Name: s.name, Aliases: s.aliases, Usage: s.usage}
			if s.def != "" {
				f.Value = strings.Split(s.def, ",")
			}
			return f, nil
		},
		bind: func(cmd *cliv3.Command, name string, dst reflect.Value) {
			dst.Set(reflect.ValueOf(cmd.StringSlice(name)))
		},
	},
}

// intFlag builds the flag both int and *int project to.
func intFlag(s fieldSpec) (cliv3.Flag, error) {
	f := &cliv3.IntFlag{Name: s.name, Aliases: s.aliases, Usage: s.usage}
	if s.def != "" {
		v, err := strconv.Atoi(s.def)
		if err != nil {
			return nil, err
		}
		f.Value = v
	}
	return f, nil
}

// flagsFor derives the flags a request type accepts from its struct tags, in
// field order, so the projected surface reads in the order the type declares.
// req may be a value or a pointer.
func flagsFor(req any) []cliv3.Flag {
	t := requestType(req)
	out := make([]cliv3.Flag, 0, t.NumField())

	for i := range t.NumField() {
		field := t.Field(i)
		name, isArg := shapeName(t, field)
		if name == "" || isArg {
			continue
		}

		kind, ok := flagKinds[field.Type]
		if !ok {
			panic(fmt.Sprintf("command: %s.%s: unsupported request field type %s; "+
				"add it to flagKinds in reflectflags.go", t, field.Name, field.Type))
		}
		f, err := kind.newFlag(fieldSpec{
			name:    name,
			aliases: tagList(field, "alias"),
			usage:   field.Tag.Get("usage"),
			def:     field.Tag.Get("default"),
		})
		if err != nil {
			panic(fmt.Sprintf("command: %s.%s: default %q is not a valid %s: %v",
				t, field.Name, field.Tag.Get("default"), field.Type, err))
		}
		out = append(out, f)
	}
	return out
}

// argsFor derives the positional arguments a request type accepts. A variadic
// field becomes an unbounded StringArgs, which is what makes it consume the
// remaining positionals rather than exactly one.
func argsFor(req any) []cliv3.Argument {
	t := requestType(req)
	out := make([]cliv3.Argument, 0, t.NumField())

	for i := range t.NumField() {
		field := t.Field(i)
		name, isArg := shapeName(t, field)
		if name == "" || !isArg {
			continue
		}

		if variadicField(field) {
			assertArgType(t, field, reflect.TypeFor[[]string]())
			out = append(out, &cliv3.StringArgs{Name: name, Max: -1})
			continue
		}
		assertArgType(t, field, reflect.TypeFor[string]())
		out = append(out, &cliv3.StringArg{Name: name})
	}
	return out
}

// bind fills req, which must be a pointer to a request struct, from the parsed
// command. Flag fields take the parsed value (or the compiled-in default when the
// flag was not given); pointer fields are written only when the flag was actually
// given, preserving set-versus-unset.
func bind(cmd *cliv3.Command, req any) error {
	v := reflect.ValueOf(req)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("command: bind needs a non-nil pointer to a request struct, got %T", req)
	}
	v = v.Elem()
	t := v.Type()

	for i := range t.NumField() {
		field := t.Field(i)
		name, isArg := shapeName(t, field)
		if name == "" {
			continue
		}

		switch {
		case isArg && variadicField(field):
			v.Field(i).Set(reflect.ValueOf(cmd.StringArgs(name)))
		case isArg:
			v.Field(i).SetString(cmd.StringArg(name))
		default:
			flagKinds[field.Type].bind(cmd, name, v.Field(i))
		}
	}
	return nil
}

// requestType resolves req to the struct type being projected, accepting a value
// or a pointer. A non-struct is a programming error at the call site.
func requestType(req any) reflect.Type {
	t := reflect.TypeOf(req)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("command: cannot project %T; a request must be a struct", req))
	}
	return t
}

// shapeName reports the projected name of a field and whether it is positional.
// An empty name means the field is excluded from the CLI surface. A field
// carrying neither tag panics rather than silently having no surface, which is
// what makes the projection a guarantee instead of a convention.
func shapeName(t reflect.Type, field reflect.StructField) (name string, isArg bool) {
	if arg, ok := field.Tag.Lookup("arg"); ok {
		return arg, true
	}
	flag, ok := field.Tag.Lookup("flag")
	if !ok {
		panic(fmt.Sprintf("command: %s.%s: field carries no flag or arg tag; "+
			`tag it, or use flag:"-" to exclude it from the CLI surface`, t, field.Name))
	}
	if flag == "-" {
		return "", false
	}
	return flag, false
}

// variadicField reports whether an arg field consumes the remaining positionals.
func variadicField(field reflect.StructField) bool {
	return field.Tag.Get("variadic") == "true"
}

// assertArgType panics unless a positional field has the type its arity requires,
// since a mismatch is a programming error reflection cannot catch at compile time.
func assertArgType(t reflect.Type, field reflect.StructField, want reflect.Type) {
	if field.Type != want {
		panic(fmt.Sprintf("command: %s.%s: a positional argument of this arity must be %s, got %s",
			t, field.Name, want, field.Type))
	}
}

// withUsage replaces the usage text of the named projected flag and returns the
// slice. It is the narrow escape hatch for a usage string that cannot be a struct
// tag because it is computed at runtime, such as an enumeration of valid values;
// naming a flag that was not projected is a programming error and panics.
func withUsage(flags []cliv3.Flag, name, usage string) []cliv3.Flag {
	for _, f := range flags {
		if f.Names()[0] != name {
			continue
		}
		switch v := f.(type) {
		case *cliv3.StringFlag:
			v.Usage = usage
		case *cliv3.StringSliceFlag:
			v.Usage = usage
		default:
			panic(fmt.Sprintf("command: withUsage does not handle flag type %T", f))
		}
		return flags
	}
	panic(fmt.Sprintf("command: withUsage: no projected flag named %q", name))
}

// tagList splits a comma-separated tag value, returning nil when it is absent.
func tagList(field reflect.StructField, key string) []string {
	v := field.Tag.Get(key)
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}
