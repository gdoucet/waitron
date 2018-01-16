package main

import (
	"errors"
	"fmt"
	"encoding/json"
	"github.com/flosch/pongo2"
	"github.com/satori/go.uuid"
	clconfig "github.com/coreos/container-linux-config-transpiler/config"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strings"
)

// Machine configuration
type Machine struct {
	Hostname        string
	OperatingSystem string
	Finish          string
	Preseed         string
	Ignition        string
	CoreosConfig    string
	ShortName       string
	Domain          string
	Token           string // This is set by the service
	Network         []Interface
	Params          map[string]string
	ImageURL        string `yaml:"image_url"`
	Kernel          string
	Initrd          string
	Cmdline         string
}

// Interface Configuration
type Interface struct {
	Name       string
	IPAddress  string
	MacAddress string
	Gateway    string
	Netmask    string
}

// PixieConfig boot configuration
type PixieConfig struct {
	Kernel  string   `json:"kernel" description:"The kernel file"`
	Initrd  []string `json:"initrd"`
	Cmdline string   `json:"cmdline"`
}

func machineDefinition(hostname string, machinePath string) (Machine, error) {
	var m Machine
	data, err := ioutil.ReadFile(path.Join(machinePath, hostname+".yaml"))
	if err != nil {
		return Machine{}, err
	}
	err = yaml.Unmarshal(data, &m)
	if err != nil {
		return Machine{}, err
	}
	hostSlice := strings.Split(m.Hostname, ".")
	m.ShortName = hostSlice[0]
	m.Domain = strings.Join(hostSlice[1:], ".")
	return m, nil
}

// Render template among with machine and config struct
func (m Machine) renderTemplate(template string, templateType string, config Config) (string, error) {

	template = path.Join(config.TemplatePath, template)
	if _, err := os.Stat(template); err != nil {
		return "", errors.New("Template does not exist")
	}

	var tpl = pongo2.Must(pongo2.FromFile(template))
	result, err := tpl.Execute(pongo2.Context{"machine": m, "config": config})

	if err != nil {
		return "", err
	}

    if templateType == "ignition" || templateType == "coreosconfig" {
        dataIn := []byte(result)

        cfg, ast, report := clconfig.Parse(dataIn)
        if len(report.Entries) > 0 {
            return "", errors.New(fmt.Sprintf("%v\n%v", report.String(), result))
        }

        ingCfg, report := clconfig.Convert(cfg, "", ast)
        if len(report.Entries) > 0 {
            return "", errors.New(report.String())
        }

        var dataOut []byte
        dataOut, err = json.Marshal(&ingCfg)
        if err != nil {
            return "", errors.New(fmt.Sprintf("Failed to marshal output: %v", err))
        }
        result = string(dataOut)
    }

	return result, err
}

// Posts machine macaddress to the forman proxy among with pxe configuration
func (m Machine) setBuildMode(config Config) error {
	// Generate a random token used to authenticate requests
	config.Tokens[m.Hostname] = uuid.Must(uuid.NewV4()).String()
	log.Println(fmt.Sprintf("%s installation token: %s", m.Hostname, config.Tokens[m.Hostname]))
	// Add token to machine struct
	m.Token = config.Tokens[m.Hostname]
	//Add to the MachineBuild table
	config.MachineBuild[fmt.Sprintf("%s", m.Network[0].MacAddress)] = m.Hostname
	//Change machine state
	config.MachineState[m.Hostname] = "Installing"
	return nil
}

/*
cancelBuild remove the machines mac address from the MachineBuild map
which stops waitron from serving the PixieConfig used by pixiecore
*/
func (m Machine) cancelBuildMode(config Config) error {
	//Delete mac from the building map
	delete(config.MachineBuild, fmt.Sprintf("%s", m.Network[0].MacAddress))
	//Change machine state
	config.MachineState[m.Hostname] = "Installed"
	return nil
}

// Return string2 if string1 is empty
func defaultString(string1 string, string2 string) string {
	if string1 == "" {
		return string2
	}
	return string1
}

// Builds pxe config to be sent to pixiecore
func (m Machine) pixieInit(config Config) (PixieConfig, error) {
	pixieConfig := PixieConfig{}
	tpl, err := pongo2.FromString(defaultString(m.Cmdline, config.DefaultCmdline))
	if err != nil {
		return pixieConfig, err
	}
    cmdline, err := tpl.Execute(pongo2.Context{"config": config, "Params": m.Params, "BaseURL": config.BaseURL, "Hostname": m.Hostname, "Token": m.Token})
	if err != nil {
		return pixieConfig, err
	}
	pixieConfig.Kernel = defaultString(m.ImageURL+m.Kernel, config.DefaultImageURL+config.DefaultKernel)
	pixieConfig.Initrd = []string{defaultString(m.ImageURL+m.Initrd, config.DefaultImageURL+config.DefaultInitrd)}
	pixieConfig.Cmdline = cmdline
	return pixieConfig, nil
}
