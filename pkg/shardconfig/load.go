package shardconfig

import (
	"path/filepath"
	"runtime"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

type RootConfig struct {
	URL string `hcl:"url,optional"`
	OCI string `hcl:"oci,optional"`
}

type RunConfig struct {
	Name string `hcl:"name,label"`
	Host bool   `hcl:"host,optional"`
}

type PathConfig struct {
	Name string `hcl:"name,label"`
	Path string `hcl:"path"`
	Into string `hcl:"into"`
}

type AdvertiseConfig struct {
	RunPaths []RunConfig  `hcl:"run,block"`
	Paths    []PathConfig `hcl:"path,block"`
}

type ServiceConfig struct {
	Name    string   `hcl:"name,label"`
	Command []string `hcl:"command"`
	Root    bool     `hcl:"as_root,optional"`
}

type Config struct {
	Name string `hcl:"name,optional"`

	Root *RootConfig `hcl:"root,block"`

	Service []*ServiceConfig `hcl:"service,block"`

	Advertisements []*AdvertiseConfig `hcl:"advertise,block"`
}

type opts struct {
	platform string
}

type Option func(*opts)

func WithPlatform(platform string) Option {
	return func(c *opts) {
		c.platform = platform
	}
}

func Load(path string, options ...Option) (*Config, error) {
	var (
		o   opts
		cfg Config
	)

	for _, f := range options {
		f(&o)
	}

	if o.platform == "" {
		o.platform = runtime.GOARCH
	}

	var ctx hcl.EvalContext

	ctx.Functions = map[string]function.Function{
		"for_platform": function.New(&function.Spec{
			Params: []function.Parameter{
				{
					Name: "map",
					Type: cty.Map(cty.DynamicPseudoType),
				},
			},
			Type: function.StaticReturnType(cty.DynamicPseudoType),
			Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
				for k, v := range args[0].AsValueMap() {
					if k == o.platform {
						return v, nil
					}
				}

				return cty.NilVal, nil
			},
		}),
	}

	ctx.Variables = map[string]cty.Value{
		"arm64": cty.StringVal("arm64"),
		"amd64": cty.StringVal("amd64"),
	}

	err := hclsimple.DecodeFile(path, &ctx, &cfg)
	if err != nil {
		return nil, err
	}

	if cfg.Name == "" {
		base := filepath.Base(path)
		ext := filepath.Ext(base)

		cfg.Name = base[:len(base)-len(ext)]
	}

	return &cfg, nil
}

func Decode(name string, data []byte, options ...Option) (*Config, error) {
	var (
		o   opts
		cfg Config
	)

	for _, f := range options {
		f(&o)
	}

	if o.platform == "" {
		o.platform = runtime.GOARCH
	}

	var ctx hcl.EvalContext

	ctx.Functions = map[string]function.Function{
		"for_platform": function.New(&function.Spec{
			Params: []function.Parameter{
				{
					Name: "map",
					Type: cty.Map(cty.DynamicPseudoType),
				},
			},
			Type: function.StaticReturnType(cty.DynamicPseudoType),
			Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
				for k, v := range args[0].AsValueMap() {
					if k == o.platform {
						return v, nil
					}
				}

				return cty.NilVal, nil
			},
		}),
	}

	ctx.Variables = map[string]cty.Value{
		"arm64": cty.StringVal("arm64"),
		"amd64": cty.StringVal("amd64"),
	}

	err := hclsimple.Decode("app.hcl", data, &ctx, &cfg)
	if err != nil {
		return nil, err
	}

	cfg.Name = name

	return &cfg, nil
}
