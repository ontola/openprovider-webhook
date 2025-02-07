// +build linux,amd64

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/jetstack/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	"github.com/jetstack/cert-manager/pkg/acme/webhook/cmd"
	metav1 "github.com/jetstack/cert-manager/pkg/apis/meta/v1"
	k8smetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var GroupName = os.Getenv("GROUP_NAME")

func main() {
	if GroupName == "" {
		panic("GROUP_NAME must be specified")
	}

	// This will register our custom DNS provider with the webhook serving
	// library, making it available as an API under the provided GroupName.
	// You can register multiple DNS provider implementations with a single
	// webhook, where the Name() method will be used to disambiguate between
	// the different implementations.
	cmd.RunWebhookServer(GroupName,
		&OpenproviderSolver{},
	)
}

type opZoneRecord struct {
	Name       string
	Prio       int
	Ttl        int
	RecordType string `json:"type"`
	Value      string
}

type opZoneRecordModificationSet struct {
	Add     []opZoneRecord
	Remove  []opZoneRecord
	Replace []opZoneRecord
	// Update opZoneRecord
}

type opUpdateZoneRequest struct {
	Name    string
	records opZoneRecordModificationSet
}

type OpenproviderSolver struct {
	// 4. ensure your webhook's service account has the required RBAC role
	//    assigned to it for interacting with the Kubernetes APIs you need.
	client kubernetes.Clientset
}

// customDNSProviderConfig is a structure that is used to decode into when
// solving a DNS01 challenge.
// This information is provided by cert-manager, and may be a reference to
// additional configuration that's needed to solve the challenge for this
// particular certificate or issuer.
// This typically includes references to Secret resources containing DNS
// provider credentials, in cases where a 'multi-tenant' DNS solver is being
// created.
// If you do *not* require per-issuer or per-certificate configuration to be
// provided to your webhook, you can skip decoding altogether in favour of
// using CLI flags or similar to provide configuration.
// You should not include sensitive information here. If credentials need to
// be used by your provider here, you should reference a Kubernetes Secret
// resource and fetch these credentials using a Kubernetes clientset.
type customDNSProviderConfig struct {
	APIKeySecretRef metav1.SecretKeySelector `json:"apiKeySecretRef"`
}

// Name is used as the name for this DNS solver when referencing it on the ACME
// Issuer resource.
// This should be unique **within the group name**, i.e. you can have two
// solvers configured with the same Name() **so long as they do not co-exist
// within a single webhook deployment**.
// For example, `cloudflare` may be used as the name of a solver.
func (c *OpenproviderSolver) Name() string {
	return "openprovider-solver"
}

// Present is responsible for actually presenting the DNS record with the
// DNS provider.
// This method should tolerate being called multiple times with the same value.
// cert-manager itself will later perform a self check to ensure that the
// solver has correctly configured the DNS provider.
func (c *OpenproviderSolver) Present(ch *v1alpha1.ChallengeRequest) error {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return err
	}

	// TODO: do something more useful with the decoded configuration
	fmt.Printf("Decoded configuration %v", cfg)
	key, err := c.getKeyFromSecret(&cfg, ch.ResourceNamespace)
	if err != nil {
		return err
	}

	body, err := json.Marshal(opUpdateZoneRequest {
		Name: ch.ResolvedFQDN,
		records: opZoneRecordModificationSet{
			Add: []opZoneRecord {
				{
					Name: ch.ResolvedFQDN,
					RecordType: "txt",
					Ttl: 200,
					Value: ch.Key,
				},
			},
		},
	})
	if err != nil {
		return err
	}

	client := &http.Client{}
	req, err := http.NewRequest(
		http.MethodPut,
		fmt.Sprintf("https://api.openprovider.eu/v1beta/dns/zones/%s", ch.ResolvedZone),
		bytes.NewBuffer(body),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *key))

	resp, err := client.Do(req)

	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("invalid status %d while updating domain", resp.StatusCode)
	}

	return nil
}

// CleanUp should delete the relevant TXT record from the DNS provider console.
// If multiple TXT records exist with the same record name (e.g.
// _acme-challenge.example.com) then **only** the record with the same `key`
// value provided on the ChallengeRequest should be cleaned up.
// This is in order to facilitate multiple DNS validations for the same domain
// concurrently.
func (c *OpenproviderSolver) CleanUp(ch *v1alpha1.ChallengeRequest) error {
	cfg, err := loadConfig(ch.Config)
	if err != nil {
		return err
	}

	fmt.Printf("Decoded configuration %v", cfg)
	key, err := c.getKeyFromSecret(&cfg, ch.ResourceNamespace)
	if err != nil {
		return err
	}

	body, err := json.Marshal(opUpdateZoneRequest {
		Name: ch.ResolvedFQDN,
		records: opZoneRecordModificationSet{
			Remove: []opZoneRecord {
				{
					Name: ch.ResolvedFQDN,
					RecordType: "txt",
					Ttl: 200,
					Value: ch.Key,
				},
			},
		},
	})
	if err != nil {
		return err
	}

	client := &http.Client{}
	req, err := http.NewRequest(
		http.MethodPut,
		fmt.Sprintf("https://api.openprovider.eu/v1beta/dns/zones/%s", ch.ResolvedZone),
		bytes.NewBuffer(body),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", *key))

	resp, err := client.Do(req)

	if err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("invalid status %d while updating domain", resp.StatusCode)
	}
	return nil
}

// Initialize will be called when the webhook first starts.
// This method can be used to instantiate the webhook, i.e. initialising
// connections or warming up caches.
// Typically, the kubeClientConfig parameter is used to build a Kubernetes
// client that can be used to fetch resources from the Kubernetes API, e.g.
// Secret resources containing credentials used to authenticate with DNS
// provider accounts.
// The stopCh can be used to handle early termination of the webhook, in cases
// where a SIGTERM or similar signal is sent to the webhook process.
func (c *OpenproviderSolver) Initialize(kubeClientConfig *rest.Config, stopCh <-chan struct{}) error {
	cl, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return err
	}

	c.client = *cl

	return nil
}

// loadConfig is a small helper function that decodes JSON configuration into
// the typed config struct.
func loadConfig(cfgJSON *extapi.JSON) (customDNSProviderConfig, error) {
	cfg := customDNSProviderConfig{}
	// handle the 'base case' where no configuration has been provided
	if cfgJSON == nil {
		return cfg, nil
	}
	if err := json.Unmarshal(cfgJSON.Raw, &cfg); err != nil {
		return cfg, fmt.Errorf("error decoding solver config: %v", err)
	}

	return cfg, nil
}

func (c *OpenproviderSolver) getKeyFromSecret(cfg *customDNSProviderConfig, namespace string) (*string, error) {
	secretName := cfg.APIKeySecretRef.LocalObjectReference.Name

	sec, err := c.client.CoreV1().Secrets(namespace).Get(context.Background(), secretName, k8smetav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	secBytes, ok := sec.Data[cfg.APIKeySecretRef.Key]
	if !ok {
		return nil, fmt.Errorf("key %q not found in secret \"%s/%s\"", cfg.APIKeySecretRef.Key,
			cfg.APIKeySecretRef.LocalObjectReference.Name, namespace)
	}

	apiKey := string(secBytes)
	return &apiKey, nil
}
