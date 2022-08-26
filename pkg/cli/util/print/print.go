package print

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"
)

func DefaultTable() *tablewriter.Table {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetBorder(false)
	table.SetRowLine(true)
	table.SetTablePadding(" ")
	table.SetNoWhiteSpace(false)
	return table
}

func PrintWithFormat(obj interface{}, format string) error {
	switch format {
	case "jsonstream":
		if reflect.TypeOf(obj).Kind() != reflect.Slice {
			return errors.New("obj type is not an array")
		}

		s := reflect.ValueOf(obj)

		for i := 0; i < s.Len(); i++ {
			bytes, err := json.Marshal(s.Index(i).Interface())
			if err != nil {
				return err
			}
			fmt.Println(string(bytes))
		}
		return nil

	case "json":
		bytes, err := json.MarshalIndent(obj, "", strings.Repeat(" ", 4))
		if err != nil {
			return err
		}
		fmt.Println(string(bytes))
		return nil

	case "yaml":
		bytes, err := yaml.Marshal(obj)
		if err != nil {
			return err
		}
		fmt.Println(string(bytes))
		return nil
	default:
		return fmt.Errorf("format (%s) not supported", format)
	}
}

// OverrideTable will override table output to json where table is not supported
func OverrideTable(c string) string {
	if c == "table" {
		return "json"
	}
	return c
}
