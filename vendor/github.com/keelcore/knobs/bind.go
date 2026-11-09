package knobs

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// Row is one key of a typed binding, as the type-match audit compares it with the schema:
// module.name, its knobs type, and whether it is secret.
type Row struct {
	Key    string `json:"key"`
	Type   string `json:"type"`
	Secret bool   `json:"secret"`
}

var (
	durationType = reflect.TypeOf(time.Duration(0))
	secretType   = reflect.TypeOf(Secret{})
)

// boundField is one tagged field of a binding struct.
type boundField struct {
	module, name string
	typ          string
	secret       bool
	value        reflect.Value
}

// fieldsOf lists the tagged fields of a pointer to a struct. A field takes `knobs:"module.name"`.
func fieldsOf(ptr any) ([]boundField, error) {
	rv := reflect.ValueOf(ptr)
	if rv.Kind() != reflect.Pointer || rv.IsNil() || rv.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("knobs: want a non-nil pointer to a struct, got %T", ptr)
	}
	sv := rv.Elem()
	var out []boundField
	for i := 0; i < sv.NumField(); i++ {
		tag, ok := sv.Type().Field(i).Tag.Lookup("knobs")
		if !ok {
			continue
		}
		mod, name, ok := strings.Cut(tag, ".")
		if !ok {
			return nil, fmt.Errorf("knobs: tag %q is not module.name", tag)
		}
		typ, secret, err := fieldType(sv.Field(i).Type())
		if err != nil {
			return nil, fmt.Errorf("knobs: field %s: %w", tag, err)
		}
		out = append(out, boundField{module: mod, name: name, typ: typ, secret: secret, value: sv.Field(i)})
	}
	return out, nil
}

// fieldType maps a Go field type to its knobs type.
func fieldType(t reflect.Type) (string, bool, error) {
	switch {
	case t == secretType:
		return TypeString, true, nil
	case t == durationType:
		return TypeDuration, false, nil
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return TypeInteger, false, nil
	case reflect.Float32, reflect.Float64:
		return TypeNumber, false, nil
	case reflect.Bool:
		return TypeBoolean, false, nil
	case reflect.String:
		return TypeString, false, nil
	}
	return "", false, fmt.Errorf("unsupported type %s", t)
}

// TableOf is the type table of one or more binding structs (pointers), in key order.
func TableOf(bindings ...any) ([]Row, error) {
	var rows []Row
	for _, b := range bindings {
		fields, err := fieldsOf(b)
		if err != nil {
			return nil, err
		}
		for _, f := range fields {
			rows = append(rows, Row{Key: f.module + "." + f.name, Type: f.typ, Secret: f.secret})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	return rows, nil
}

// Bind copies the configuration into a binding struct (a pointer): each tagged field takes its
// key's value. A field whose type does not match its property's fails; an unset key leaves its
// field as it is.
func (c *Config) Bind(ptr any) error {
	fields, err := fieldsOf(ptr)
	if err != nil {
		return err
	}
	for _, f := range fields {
		if err := c.bindField(f); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) bindField(f boundField) error {
	p := c.set.prop(f.module, f.name)
	key := c.set.key(f.module, f.name)
	if p == nil {
		return fmt.Errorf("knobs: %s is not in the schemas", key)
	}
	if p.typ != f.typ || p.secret != f.secret {
		return fmt.Errorf("knobs: %s is %s (secret %v); the field is %s (secret %v)", key, p.typ, p.secret, f.typ, f.secret)
	}
	v, ok := c.Get(key)
	if !ok {
		return nil
	}
	switch {
	case f.secret:
		s, _ := v.Value.(string)
		f.value.Set(reflect.ValueOf(NewSecret(s)))
	case f.typ == TypeDuration:
		d, err := time.ParseDuration(v.Value.(string))
		if err != nil {
			return fmt.Errorf("knobs: %s: %w", key, err)
		}
		f.value.SetInt(int64(d))
	case f.typ == TypeInteger:
		f.value.SetInt(v.Value.(int64))
	case f.typ == TypeNumber:
		n, _ := asFloat(v.Value)
		f.value.SetFloat(n)
	case f.typ == TypeBoolean:
		f.value.SetBool(v.Value.(bool))
	default:
		f.value.SetString(v.Value.(string))
	}
	return nil
}
