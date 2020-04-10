package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"text/template"

	"github.com/google/go-jsonnet"
	"github.com/google/go-jsonnet/ast"
	//yaml "gopkg.in/yaml.v2"
)

func addNativeFuncs(vm *jsonnet.VM) error {
	vm.NativeFunction(&jsonnet.NativeFunction{
		Name: "parsejson",
		Params: ast.Identifiers{"json"},
		Func: parsejson_native,
	})
	/*
	vm.NativeFunction(&jsonnet.NativeFunction{
		Name: "parseyaml",
		Params: ast.Identifiers{"yaml"},
		Func: parseyaml_native,
	})
	*/
	vm.NativeFunction(&jsonnet.NativeFunction{
		Name:   "query_escape",
		Params: ast.Identifiers{"s"},
		Func:   query_escape_native,
	})
	vm.NativeFunction(&jsonnet.NativeFunction{
		Name:   "template",
		Params: ast.Identifiers{"template", "value"},
		Func:   template_native,
	})
	return nil
}

func parsejson_native(datastring []interface{}) (interface{}, error) {
	jsonstr, ok := datastring[0].(string)
	if !ok {
		return nil, errors.New("json argument is not a string")
	}
	var obj interface{}
	if err := json.Unmarshal([]byte(jsonstr), &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

/*
func parseyaml_native(datastring []interface{}) (interface{}, error) {
	yamlstr, ok := datastring[0].(string)
	if !ok {
		return nil, errors.New("yaml argument is not a string")
	}
	var obj interface{}
	if err := yaml.Unmarshal([]byte(yamlstr), &obj); err != nil {
		return nil, err
	}
	return fixYamlTypes(obj), nil
}
*/

func query_escape_native(datastring []interface{}) (interface{}, error) {
	s, ok := datastring[0].(string)
	if !ok {
		return nil, errors.New("s argument is not a string")
	}
	return url.QueryEscape(s), nil
}

func template_native(datastring []interface{}) (interface{}, error) {
	templatestr, ok := datastring[0].(string)
	if !ok {
		return nil, errors.New("template argument is not a string")
	}
	template, err := template.New("template").Parse(templatestr)
	if err != nil {
		return nil, err
	}
	buf := new(bytes.Buffer)
	if err := template.Execute(buf, datastring[1]); err != nil {
		return nil, err
	}
	return buf.String(), nil
}

func fixYamlTypes(i interface{}) interface{} {
	switch x := i.(type) {
	case map[interface{}]interface{}:
		m := map[string]interface{}{}
		for k, v := range x {
			m[k.(string)] = fixYamlTypes(v)
		}
		return m
	case []interface{}:
		for i, v := range x {
			x[i] = fixYamlTypes(v)
		}
	case int:
		return float64(x)
	}
	return i
}
