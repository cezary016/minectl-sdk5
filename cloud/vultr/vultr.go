package vultr

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/dirien/minectl-sdk/automation"
	"github.com/dirien/minectl-sdk/cloud"
	"github.com/dirien/minectl-sdk/common"
	minctlTemplate "github.com/dirien/minectl-sdk/template"
	"github.com/dirien/minectl-sdk/update"
	"github.com/vultr/govultr/v2"
	"golang.org/x/oauth2"
)

type Vultr struct {
	client *govultr.Client
	tmpl   *minctlTemplate.Template
}

func NewVultr(apiKey string) (*Vultr, error) {
	config := &oauth2.Config{}
	ctx := context.Background()
	ts := config.TokenSource(ctx, &oauth2.Token{AccessToken: apiKey})
	vultrClient := govultr.NewClient(oauth2.NewClient(ctx, ts))
	tmpl, err := minctlTemplate.NewTemplateBash()
	if err != nil {
		return nil, err
	}
	vultr := &Vultr{
		client: vultrClient,
		tmpl:   tmpl,
	}
	return vultr, nil
}

func (v *Vultr) CreateServer(args automation.ServerArgs) (*automation.ResourceResults, error) {
	publicKey, err := cloud.GetSSHPublicKey(args)
	if err != nil {
		return nil, err
	}
	sshKey, err := v.client.SSHKey.Create(context.Background(), &govultr.SSHKeyReq{
		SSHKey: *publicKey,
		Name:   fmt.Sprintf("%s-ssh", args.MinecraftResource.GetName()),
	})
	if err != nil {
		return nil, err
	}

	script, err := v.tmpl.GetTemplate(args.MinecraftResource, &minctlTemplate.CreateUpdateTemplateArgs{Name: minctlTemplate.GetTemplateBashName(args.MinecraftResource.IsProxyServer())})
	if err != nil {
		return nil, err
	}
	startupScript, err := v.client.StartupScript.Create(context.Background(), &govultr.StartupScriptReq{
		Script: base64.StdEncoding.EncodeToString([]byte(script)),
		Name:   fmt.Sprintf("%s-stackscript", args.MinecraftResource.GetName()),
		Type:   "boot",
	})
	if err != nil {
		return nil, err
	}

	ubuntu2204Id := 1743
	opts := &govultr.InstanceCreateReq{
		SSHKeys:  []string{sshKey.ID},
		ScriptID: startupScript.ID,
		Hostname: args.MinecraftResource.GetName(),
		Label:    args.MinecraftResource.GetName(),
		Region:   args.MinecraftResource.GetRegion(),
		Plan:     args.MinecraftResource.GetSize(),
		OsID:     ubuntu2204Id,
		Tags: []string{
			common.InstanceTag,
			args.MinecraftResource.GetEdition(),
		},
	}

	instance, err := v.client.Instance.Create(context.Background(), opts)
	if err != nil {
		return nil, err
	}

	stillCreating := true
	for stillCreating {
		instance, err = v.client.Instance.Get(context.Background(), instance.ID)
		if err != nil {
			return nil, err
		}
		if instance.Status == "active" {
			stillCreating = false
			time.Sleep(2 * time.Second)
		} else {
			time.Sleep(2 * time.Second)
		}
	}
	return &automation.ResourceResults{
		ID:       instance.ID,
		Name:     instance.Label,
		Region:   instance.Region,
		PublicIP: instance.MainIP,
		Tags:     strings.Join(instance.Tags, ","),
	}, err
}

func (v *Vultr) DeleteServer(id string, args automation.ServerArgs) error {
	sshKeys, _, err := v.client.SSHKey.List(context.Background(), nil)
	if err != nil {
		return err
	}
	for _, sshKey := range sshKeys {
		if sshKey.Name == fmt.Sprintf("%s-ssh", args.MinecraftResource.GetName()) {
			err := v.client.SSHKey.Delete(context.Background(), sshKey.ID)
			if err != nil {
				return err
			}
			break
		}
	}
	err = v.client.Instance.Delete(context.Background(), id)
	if err != nil {
		return err
	}
	return nil
}

func (v *Vultr) ListServer() ([]automation.ResourceResults, error) {
	instances, _, err := v.client.Instance.List(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	var result []automation.ResourceResults
	for _, instance := range instances {
		for _, tag := range instance.Tags {
			if strings.Contains(tag, common.InstanceTag) {
				result = append(result, automation.ResourceResults{
					ID:       instance.ID,
					Name:     instance.Label,
					Region:   instance.Region,
					PublicIP: instance.MainIP,
					Tags:     strings.Join(instance.Tags, ","),
				})
			}
		}
	}
	return result, nil
}

func (v *Vultr) UpdateServer(id string, args automation.ServerArgs) error {
	instance, err := v.client.Instance.Get(context.Background(), id)
	if err != nil {
		return err
	}

	remoteCommand := update.NewRemoteServer(args.SSHPrivateKeyPath, instance.MainIP, "root")
	err = remoteCommand.UpdateServer(args.MinecraftResource)
	if err != nil {
		return err
	}
	return nil
}

func (v *Vultr) UploadPlugin(id string, args automation.ServerArgs, plugin, destination string) error {
	instance, err := v.client.Instance.Get(context.Background(), id)
	if err != nil {
		return err
	}
	remoteCommand := update.NewRemoteServer(args.SSHPrivateKeyPath, instance.MainIP, "root")
	err = remoteCommand.TransferFile(plugin, filepath.Join(destination, filepath.Base(plugin)), args.MinecraftResource.GetSSHPort())
	if err != nil {
		return err
	}
	_, err = remoteCommand.ExecuteCommand("systemctl restart minecraft.service", args.MinecraftResource.GetSSHPort())
	if err != nil {
		return err
	}
	return nil
}

func (v *Vultr) GetServer(id string, _ automation.ServerArgs) (*automation.ResourceResults, error) {
	instance, err := v.client.Instance.Get(context.Background(), id)
	if err != nil {
		return nil, err
	}

	return &automation.ResourceResults{
		ID:       instance.ID,
		Name:     instance.Label,
		Region:   instance.Region,
		PublicIP: instance.MainIP,
		Tags:     strings.Join(instance.Tags, ","),
	}, err
}
