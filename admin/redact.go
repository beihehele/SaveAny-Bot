package admin

import (
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/krau/SaveAny-Bot/config"
)

var textURL = regexp.MustCompile(`https?://[^\s<>"']+`)

func configSecrets(cfg config.Config) []string {
	var result []string
	var collect func(reflect.Value, string)
	collect = func(value reflect.Value, name string) {
		if !value.IsValid() {
			return
		}
		switch value.Kind() {
		case reflect.Pointer, reflect.Interface:
			if !value.IsNil() {
				collect(value.Elem(), name)
			}
		case reflect.Struct:
			for i := 0; i < value.NumField(); i++ {
				collect(value.Field(i), value.Type().Field(i).Name)
			}
		case reflect.Map:
			iterator := value.MapRange()
			for iterator.Next() {
				if iterator.Key().Kind() == reflect.String {
					collect(iterator.Value(), iterator.Key().String())
				}
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < value.Len(); i++ {
				collect(value.Index(i), name)
			}
		case reflect.String:
			name = strings.ToLower(name)
			if strings.Contains(name, "password") || strings.Contains(name, "secret") || strings.Contains(name, "token") ||
				strings.Contains(name, "key") || name == "apphash" || name == "cookie" || name == "authorization" {
				if secret := value.String(); secret != "" {
					result = append(result, secret)
				}
			}
		}
	}
	collect(reflect.ValueOf(cfg), "")
	sort.Slice(result, func(i, j int) bool { return len(result[i]) > len(result[j]) })
	return result
}

func (s *Server) redact(text string) string {
	text = textURL.ReplaceAllStringFunc(text, func(raw string) string {
		value, err := url.Parse(raw)
		if err != nil {
			return "[redacted URL]"
		}
		value.User, value.RawQuery, value.Fragment = nil, "", ""
		return value.String()
	})
	for _, secret := range s.secrets {
		text = strings.ReplaceAll(text, secret, "[redacted]")
	}
	return text
}
