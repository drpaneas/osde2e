package load

import (
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/markbates/pkger"
	"github.com/openshift/osde2e/pkg/common/util"
	"gopkg.in/yaml.v2"
)

const (
	// EnvVarTag is the Go struct tag containing the environment variable that sets the option.
	EnvVarTag = "env"

	// SectionTag is the Go struct tag containing the documentation section of the option.
	SectionTag = "sect"

	// DefaultTag is the Go struct tag containing the default value of the option.
	DefaultTag = "default"
)

// Look for fields looking to have a little randomness injected
var rndStringRegex = regexp.MustCompile("__RND_(\\d+)__")

func init() {
	rand.Seed(time.Now().Unix())
}

// IntoObject populates an object based on the tags specified in the object.
func IntoObject(object interface{}, configs []string, customConfig string) error {
	if objectType := reflect.TypeOf(object); objectType.Kind() != reflect.Ptr {
		return fmt.Errorf("the supplied object must be a pointer")
	}

	// Populate the defaults first, then read the YAML, then override with the environment
	// 1. Load defaults
	if err := loadDefaults(object); err != nil {
		return fmt.Errorf("error loading config defaults: %v", err)
	}
	// 2a. Pre-canned YAML configs
	for _, config := range configs {
		if err := loadYAMLFromConfigs(object, config); err != nil {
			return fmt.Errorf("error loading config from YAML: %v", err)
		}
	}

	// 2b. Custom YAML configs
	if customConfig != "" {
		log.Printf("Custom YAML config provided, loading from %s", customConfig)
		if err := loadYAMLFromFile(object, customConfig); err != nil {
			return fmt.Errorf("error loading custom config from YAML: %v", err)
		}
	}

	// 3. Load config from environment.
	// Reiterating: Environment variables take precedence over YAML.
	if err := loadFromEnv(object); err != nil {
		return fmt.Errorf("error loading config from environment: %v", err)
	}

	return nil
}

// load values into the given field
func load(v reflect.Value, source string) error {
	var setValue string
	var ok bool
	for i := 0; i < v.Type().NumField(); i++ {
		f := v.Type().Field(i)

		if f.Type.Kind() == reflect.Struct {
			// Specific to supporting AddOns via ENV
			load(v.FieldByIndex(f.Index), source)
		} else {
			if source == "default" {
				if setValue, ok = f.Tag.Lookup(DefaultTag); !ok {
					continue
				}
			}
			if source == "env" {
				if env, ok := f.Tag.Lookup(EnvVarTag); ok {
					if setValue = os.Getenv(env); setValue == "" {
						continue
					}
				}
			}

			field := v.Field(i)
			if err := processValueFromString(f, field, setValue); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadDefaults takes default values from the annotations in the types
// file and assigns them to the appropriate config option.
// It also works on handling special cases for default loading.
func loadDefaults(object interface{}) error {
	v := reflect.ValueOf(object).Elem()
	load(v, "default")
	return nil
}

// loadYAMLFromConfigs accepts a config name and attempts to unmarshal the config from the /configs directory.
func loadYAMLFromConfigs(object interface{}, name string) error {
	var file http.File
	var data []byte
	var err error

	if file, err = pkger.Open(filepath.Join("/configs", name+".yaml")); err != nil {
		return fmt.Errorf("error trying to open config %s: %v", name, err)
	}

	if data, err = ioutil.ReadAll(file); err != nil {
		return err
	}

	if err = yaml.Unmarshal(data, object); err != nil {
		return err
	}

	return nil
}

// loadYAMLFromFile accepts file info and attempts to unmarshal the file into the // config.
func loadYAMLFromFile(object interface{}, name string) error {
	var data []byte
	var err error
	var dir, path string

	if dir, err = os.Getwd(); err != nil {
		log.Fatalf("Unable to get CWD: %s", err.Error())
	}
	// TODO: This needs to change once we stop branching out execution the way we do it currently
	// It's fragile
	if path, err = filepath.Abs(filepath.Join(dir, name)); err != nil {
		return err
	}

	path = filepath.Clean(path)

	if data, err = ioutil.ReadFile(path); err != nil {
		return err
	}

	if err = yaml.Unmarshal(data, object); err != nil {
		return err
	}

	return nil
}

// loadFromEnv sets values from environment variables specified in `env` tags.
func loadFromEnv(object interface{}) error {
	v := reflect.ValueOf(object).Elem()
	load(v, "env")

	return nil
}

func processValueFromString(f reflect.StructField, field reflect.Value, value string) error {
	switch f.Type.Kind() {
	case reflect.String:
		// Add special processing for the __TMP_DIR__ string so that directory creation is handled
		// internally to config loading.
		if value == "__TMP_DIR__" {
			if dir, err := ioutil.TempDir("", "osde2e"); err == nil {
				log.Printf("Generated temporary directory %s for field %s", dir, f.Name)
				field.SetString(dir)
			} else {
				return fmt.Errorf("error generating temporary directory for field %s: %v", f.Name, err)
			}
		} else if rndStringRegex.MatchString(value) {
			if rndStringLen, err := strconv.Atoi(rndStringRegex.FindStringSubmatch(value)[1]); err == nil {
				rndString := util.RandomStr(rndStringLen)
				log.Printf("Generated random string %s for field %s", rndString, f.Name)
				field.SetString(rndString)
			} else {
				return fmt.Errorf("error generating random string for field %s: %v", f.Name, err)
			}
		} else {
			field.SetString(value)
		}
	case reflect.Bool:
		if newBool, err := strconv.ParseBool(value); err == nil {
			field.SetBool(newBool)
		} else {
			return fmt.Errorf("error parsing bool value for field %s: %v", f.Name, err)
		}
	case reflect.Slice:
		fallthrough
	case reflect.Array:
		if value != "" {
			value := string(value)
			a := strings.Split(value, ",")
			for i := range a {
				field.Set(reflect.Append(field, reflect.ValueOf(a[i])))
			}
		}
		// We shouldn't be setting any slices with string vars
		// Specifically, Addons and Kubeconfig Contents
	case reflect.Int:
		fallthrough
	case reflect.Int64:
		if num, err := strconv.ParseInt(value, 10, 0); err == nil {
			field.SetInt(num)
		} else {
			return fmt.Errorf("error parsing int value for field %s: %v", f.Name, err)
		}
	}
	return nil
}
