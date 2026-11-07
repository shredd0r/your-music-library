package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SetActivePreset changes active_preset in the config file at path and
// returns the new config. Only that key changes; comments and the rest of
// the file are kept. A missing file is created with just this key. The
// result is validated before it is written.
func SetActivePreset(path, name string) (Config, error) {
	return editFile(path, func(root *yaml.Node) error {
		setKey(root, "active_preset", name)
		return nil
	})
}

// AddPlugin registers plugin name at url, enabled, in the config file at
// path (kept like SetActivePreset does). A name that is already
// registered is an error.
func AddPlugin(path, name, url string) (Config, error) {
	if err := ValidatePlugin(name, url); err != nil {
		return Config{}, err
	}
	return editFile(path, func(root *yaml.Node) error {
		reg := mapping(mapping(root, "plugins"), "registered")
		if find(reg, name) != nil {
			return fmt.Errorf("plugin %q is already registered", name)
		}
		entry := &yaml.Node{Kind: yaml.MappingNode}
		setKey(entry, "url", url)
		setBool(entry, "enabled", true)
		reg.Content = append(reg.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, entry)
		return nil
	})
}

// RemovePlugin removes plugin name from the config file at path.
func RemovePlugin(path, name string) (Config, error) {
	return editFile(path, func(root *yaml.Node) error {
		reg := registered(root)
		for i := 0; reg != nil && i+1 < len(reg.Content); i += 2 {
			if reg.Content[i].Value == name {
				reg.Content = append(reg.Content[:i], reg.Content[i+2:]...)
				return nil
			}
		}
		return fmt.Errorf("no plugin %q is registered", name)
	})
}

// SetPluginEnabled enables or disables plugin name in the config file at
// path.
func SetPluginEnabled(path, name string, enabled bool) (Config, error) {
	return editFile(path, func(root *yaml.Node) error {
		entry := find(registered(root), name)
		if entry == nil || entry.Kind != yaml.MappingNode {
			return fmt.Errorf("no plugin %q is registered", name)
		}
		setBool(entry, "enabled", enabled)
		return nil
	})
}

// registered returns the plugins.registered mapping, or nil.
func registered(root *yaml.Node) *yaml.Node {
	plugins := find(root, "plugins")
	if plugins == nil || plugins.Kind != yaml.MappingNode {
		return nil
	}
	if reg := find(plugins, "registered"); reg != nil && reg.Kind == yaml.MappingNode {
		return reg
	}
	return nil
}

// editFile applies change to the YAML tree of the config file at path,
// validates the result and writes it atomically. Comments are kept. A
// missing or empty file starts as an empty mapping.
func editFile(path string, change func(root *yaml.Node) error) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if doc.Kind == 0 { // empty file
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return Config{}, fmt.Errorf("%s: top level is not a mapping", path)
	}
	if err := change(root); err != nil {
		return Config{}, err
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return Config{}, err
	}
	if err := enc.Close(); err != nil {
		return Config{}, err
	}
	c, err := Parse(buf.Bytes())
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, writeFileAtomic(path, buf.Bytes())
}

// find returns the value of key in mapping m, or nil.
func find(m *yaml.Node, key string) *yaml.Node {
	for i := 0; m != nil && i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// mapping returns the mapping under key in m, creating it (or replacing
// an empty value) if needed.
func mapping(m *yaml.Node, key string) *yaml.Node {
	if v := find(m, key); v != nil {
		if v.Kind != yaml.MappingNode {
			v.Kind, v.Tag, v.Value, v.Style, v.Content = yaml.MappingNode, "!!map", "", 0, nil
		}
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	return v
}

func setBool(m *yaml.Node, key string, value bool) {
	setKey(m, key, fmt.Sprint(value))
	v := find(m, key)
	v.Tag = "!!bool"
}

func setKey(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			v := m.Content[i+1]
			v.Kind, v.Tag, v.Value, v.Style, v.Content = yaml.ScalarNode, "!!str", value, 0, nil
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}
