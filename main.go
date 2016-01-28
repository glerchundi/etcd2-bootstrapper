package main

import (
	"os"
	"strings"

	flag "github.com/spf13/pflag"
	"github.com/glerchundi/etcd2-bootstrapper/pkg"
)

const (
	cliName        = "etcd2-bootstrapper"
	cliDescription = "etcd2-bootstrapper bootstrapps an etcd2 cluster."
)

func main() {
	// configuration
	cfg := pkg.NewConfig()

	// flags
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&cfg.Me, "me", cfg.Me, "me.")
	fs.StringVar(&cfg.Members, "members", cfg.Members, "members.")
	fs.StringVar(&cfg.TmplListenUrl, "tmpl-listen-url", cfg.TmplListenUrl, "tmpl-listen-url.")
	fs.StringVar(&cfg.TmplPeerUrl, "tmpl-peer-url", cfg.TmplPeerUrl, "tmpl-peer-url.")
	fs.StringVar(&cfg.CertFile, "cert-file", cfg.CertFile, "Identify HTTPS client using this SSL certificate file.")
	fs.StringVar(&cfg.KeyFile, "key-file", cfg.KeyFile, "Identify HTTPS client using this SSL key file.")
	fs.StringVar(&cfg.CAFile, "ca-file", cfg.CAFile, "Verify certificates of HTTPS-enabled servers using this CA bundle.")
	fs.BoolVar(&cfg.Force, "force", cfg.Force, "force.")
	fs.StringVar(&cfg.Out, "out", cfg.Out, "etcd2 peers environment file destination")
	fs.SetNormalizeFunc(
		func(f *flag.FlagSet, name string) flag.NormalizedName {
			if strings.Contains(name, "_") {
				return flag.NormalizedName(strings.Replace(name, "_", "-", -1))
			}
			return flag.NormalizedName(name)
		},
	)

	// parse
	fs.Parse(os.Args[1:])

	// set from env (if present)
	fs.VisitAll(func(f *flag.Flag) {
		if !f.Changed {
			key := strings.ToUpper(strings.Join(
				[]string{
					cliName,
					strings.Replace(f.Name, "-", "_", -1),
				},
				"_",
			))
			val := os.Getenv(key)
			if val != "" {
				fs.Set(f.Name, val)
			}
		}
	})

	// and then, run!
	eb := pkg.NewEtcd2Bootstrapper(cfg)
	eb.Run()
}
