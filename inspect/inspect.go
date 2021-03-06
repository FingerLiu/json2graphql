package inspect

/* 解析 json， 提取类型并存入结构体
假定 json 文件最外层是大括号
先不处理有 list 的情况
文件名不支持中文
*/
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"text/template"
	"unicode"

	"github.com/luojilab/json2graphql/utils"
)

func unmarshal(jsonRaw []byte) (interface{}, error) {
	var result interface{}
	var err error

	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(jsonRaw))
	decoder.UseNumber()
	err = decoder.Decode(&result)

	if err != nil {
		return nil, err
	}
	return result, nil
}

type Node struct {
	Name      string
	ValueKind reflect.Kind
	ValueType reflect.Type
	InnerKind reflect.Kind /* 只有 ValueKind 为 Slice 时有值 */
	InnerType GqlType      /* 只有 ValueKind 为 Slice 且 ValueKind 为 slice 时有值 */
	Children  *[]Node
}

func (n *Node) RealType() string {
	var fieldMapping = map[reflect.Kind]string{
		reflect.Int:       "Int",
		reflect.Float32:   "Float",
		reflect.Float64:   "Float64",
		reflect.String:    "String",
		reflect.Bool:      "Boolean",
		reflect.Map:       "Map",
		reflect.Slice:     "[]",
		reflect.Interface: "String # TODO check this field",
	}
	realType := fieldMapping[n.ValueKind]
	if realType == "Map" {
		realType = uppercaseFirst(n.Name)
	} else if realType == "[]" {
		if n.InnerKind == reflect.Map {
			realType = fmt.Sprintf("[%s]", n.InnerType.Name)
		} else {
			if n.InnerKind == reflect.Interface {
				// 处理空list 的注释问题
				realType = fmt.Sprintf("[String] # TODO check this field")
			} else {
				realType = fmt.Sprintf("[%s]", fieldMapping[n.InnerKind])
			}
		}
	} else if realType == "String" {
		if n.ValueType == reflect.TypeOf(123) {
			realType = "Int"
		} else if n.ValueType == reflect.TypeOf(123.4) {
			realType = "Float64"
		}
	}
	return realType
}

type GqlType struct {
	Name     string
	Children *[]Node
}

func uppercaseFirst(s string) string {
	if s == "" {
		return ""
	}

	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func getRootType(s string) string {
	ext := filepath.Ext(s)
	return uppercaseFirst(s[0 : len(s)-len(ext)])
}

func ensureAndAppend(ptr *[]Node, node Node) *[]Node {
	if ptr == nil {
		arr := make([]Node, 0)
		ptr = &arr
	}
	*ptr = append(*ptr, node)
	return ptr
}

func guessIntOrFloat(value interface{}) reflect.Type {
	numberValue := value.(json.Number)
	var guessedType reflect.Type
	guessedType = reflect.TypeOf("str")
	_, err := numberValue.Int64()
	if err == nil {
		guessedType = reflect.TypeOf(123)
	} else {
		_, err = numberValue.Float64()
		if err == nil {
			guessedType = reflect.TypeOf(123.4)
		}
	}
	return guessedType

}

func parseList(obj interface{}, gqlTypesPtr *[]GqlType, gqlType GqlType, node Node) {
	// 解析 list 类型
	fmt.Printf("node.Name %v\n", node.Name)
	innterList := obj.([]interface{})
	if len(innterList) == 0 || innterList == nil {
		node.InnerKind = reflect.Interface
	} else {
		// 如果列表不为空，取第一个元素列表判断内部元素类型
		firstItem := innterList[0]
		itemKind := reflect.TypeOf(firstItem).Kind()

		node.InnerKind = itemKind
		// map 或简单类型 或 list ?
		if itemKind == reflect.Map {
			// item 的 type 加入 types
			innerGqlType := GqlType{Name: uppercaseFirst(node.Name)}
			Parse(firstItem, gqlTypesPtr, innerGqlType, node)
			node.InnerType = innerGqlType
		}
	}
	*gqlTypesPtr = append(*gqlTypesPtr, gqlType)
}

func Parse(obj interface{}, gqlTypesPtr *[]GqlType, gqlType GqlType, node Node) {

	numberType := reflect.TypeOf(json.Number(""))

	for key, value := range obj.(map[string]interface{}) {

		var valueKind reflect.Kind
		if value == nil {
			valueKind = reflect.Interface
		} else {
			valueKind = reflect.TypeOf(value).Kind()
		}

		child := Node{Name: key, ValueKind: valueKind}

		if reflect.TypeOf(value) == numberType {
			child.ValueType = guessIntOrFloat(value)
		}

		if value != nil && valueKind == reflect.Map {

			childGqlType := GqlType{Name: uppercaseFirst(key)}
			Parse(value, gqlTypesPtr, childGqlType, child)

		} else if valueKind == reflect.Slice {
			// 解析 list 类型
			innterList := value.([]interface{})
			if len(innterList) == 0 || innterList == nil {
				child.InnerKind = reflect.Interface

			} else {
				// 如果列表不为空，取第一个元素列表判断内部元素类型
				firstItem := innterList[0]
				itemKind := reflect.TypeOf(firstItem).Kind()

				child.InnerKind = itemKind
				// map 或简单类型 或 list ?
				if itemKind == reflect.Map {
					// item 的 type 加入 types
					childGqlType := GqlType{Name: uppercaseFirst(key)}
					Parse(firstItem, gqlTypesPtr, childGqlType, child)
					child.InnerType = childGqlType
				}
			}
		}
		// 为父类型添加子类型
		gqlType.Children = ensureAndAppend(gqlType.Children, child)

		// 为父节点添加当前节点
		node.Children = ensureAndAppend(node.Children, child)
	}
	*gqlTypesPtr = append(*gqlTypesPtr, gqlType)
}

func GenerateSchema(gqlTypes []GqlType, tmpl string, output string) error {
	tname := filepath.Base(tmpl)
	f, err := os.Create(output)
	if err != nil {
		fmt.Println("create file: ", err)
		return err
	}

	defer f.Close()
	t := template.Must(template.New(tname).Funcs(template.FuncMap{

		"Deref": func(children *[]Node) []Node {
			if children != nil {
				return *children
			} else {
				return nil
			}
		},
	}).ParseFiles(tmpl))

	m := map[string]interface{}{"gqlTypes": gqlTypes}
	err = t.ExecuteTemplate(f, tname, m)
	if err != nil {
		fmt.Println(err)
		return err
	}
	return nil

}

func GenerateSchema2Byte(gqlTypes []GqlType, tmpl string) ([]byte, error) {
	var buf bytes.Buffer
	// writer := bufio.NewWriter(&buf)
	tname := filepath.Base(tmpl)
	t := template.Must(template.New(tname).Funcs(template.FuncMap{

		"Deref": func(children *[]Node) []Node {
			if children != nil {
				return *children
			} else {
				return nil
			}
		},
	}).ParseFiles(tmpl))

	m := map[string]interface{}{"gqlTypes": gqlTypes}
	err := t.ExecuteTemplate(&buf, tname, m)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	return buf.Bytes(), nil
}

func inspect(input []byte, output string, name string) error {
	// TODO 支持多个文件
	tmpl := "inspect/templates/schema.gotpl"
	rootObj, err := unmarshal(input)

	if err != nil {
		fmt.Printf("error: %v", err.Error())
		return err
	}

	t := reflect.TypeOf(rootObj).Kind()

	mapKind := reflect.Map

	root := Node{Name: "root", ValueKind: t}
	rootType := GqlType{Name: name + "Result"}
	gqlTypes := make([]GqlType, 0)
	gqlTypesPtr := &gqlTypes
	if t == mapKind {
		Parse(rootObj, gqlTypesPtr, rootType, root)
		err = GenerateSchema(gqlTypes, tmpl, output)
		if err != nil {
			fmt.Printf("error: %v", err.Error())
			return err
		}

	} else {
		// TODO 顶层为 list
		panic("not supported root type")
	}
	return nil
}

func inspect2Bytes(input []byte, name string) ([]byte, error) {
	// TODO 支持多个文件
	tmpl := "inspect/templates/schema.gotpl"
	rootObj, err := unmarshal(input)
	fmt.Printf("root obj is %v\n", rootObj)
	var result []byte

	if err != nil {
		fmt.Printf("error: %v", err.Error())
		return nil, err
	}

	t := reflect.TypeOf(rootObj).Kind()

	mapKind := reflect.Map

	root := Node{Name: "root", ValueKind: t}
	rootType := GqlType{Name: name + "Result"}
	gqlTypes := make([]GqlType, 0)
	gqlTypesPtr := &gqlTypes
	if t == mapKind {
		Parse(rootObj, gqlTypesPtr, rootType, root)
		result, err = GenerateSchema2Byte(gqlTypes, tmpl)
		if err != nil {
			fmt.Printf("error: %v\n", err.Error())
			return nil, err
		}

	} else {
		// TODO 顶层为 list
		panic("not supported root type")
	}
	return result, nil
}

func InspectWithFile(filename, output string) error {
	input, err := ioutil.ReadFile(filename)
	if err != nil {
		fmt.Println(err)
		return err
	}
	name := getRootType(filename)
	if err := inspect(input, output, name); err != nil {
		fmt.Printf("error")
		return err
	}
	return nil
}

func InspectWithUrl(requestUrl, output, token string) error {
	// TODO 支持传入参数进行请求
	var params url.Values
	if input, code, err := utils.HttpGet(requestUrl, params, token); err != nil {
		fmt.Printf("error of %s", err)
		return err
	} else if code != 200 {
		fmt.Printf("request error of status code: %d", code)
		return nil
	} else {
		name := getRootType(output)
		if err := inspect(input, output, name); err != nil {
			return err
		}
	}
	return nil
}

func InspectWithBytes(input []byte) ([]byte, error) {
	name := "Root"
	var err error
	var result []byte
	result, err = inspect2Bytes(input, name)
	if err != nil {
		fmt.Println(err)
		return nil, err
	}
	return result, nil
}
