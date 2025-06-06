/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package client

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pkg/errors"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	clusterctlv1 "sigs.k8s.io/cluster-api/cmd/clusterctl/api/v1alpha3"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/cluster"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/config"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/repository"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/client/tree"
	yaml "sigs.k8s.io/cluster-api/cmd/clusterctl/client/yamlprocessor"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/internal/scheme"
	"sigs.k8s.io/cluster-api/cmd/clusterctl/internal/test"
)

// TestNewFakeClient is a fake test to document fakeClient usage.
func TestNewFakeClient(_ *testing.T) {
	// create a fake config with a provider named P1 and a variable named var
	repository1Config := config.NewProvider("p1", "url", clusterctlv1.CoreProviderType)

	ctx := context.Background()

	config1 := newFakeConfig(ctx).
		WithVar("var", "value").
		WithProvider(repository1Config)

	// create a fake repository with some YAML files in it (usually matching the list of providers defined in the config)
	repository1 := newFakeRepository(ctx, repository1Config, config1).
		WithPaths("root", "components").
		WithDefaultVersion("v1.0").
		WithFile("v1.0", "components.yaml", []byte("content"))

	// create a fake cluster, eventually adding some existing runtime objects to it
	cluster1 := newFakeCluster(cluster.Kubeconfig{Path: "cluster1"}, config1).
		WithObjs()

	// create a new fakeClient that allows to execute tests on the fake config, the fake repositories and the fake cluster.
	newFakeClient(context.Background(), config1).
		WithRepository(repository1).
		WithCluster(cluster1)
}

type fakeClient struct {
	configClient config.Client
	// mapping between kubeconfigPath/context with cluster client
	clusters       map[cluster.Kubeconfig]cluster.Client
	repositories   map[string]repository.Client
	internalClient *clusterctlClient
}

var _ Client = &fakeClient{}

func (f fakeClient) GetProvidersConfig() ([]Provider, error) {
	return f.internalClient.GetProvidersConfig()
}

func (f fakeClient) GetProviderComponents(ctx context.Context, provider string, providerType clusterctlv1.ProviderType, options ComponentsOptions) (Components, error) {
	return f.internalClient.GetProviderComponents(ctx, provider, providerType, options)
}

func (f fakeClient) GenerateProvider(ctx context.Context, provider string, providerType clusterctlv1.ProviderType, options ComponentsOptions) (Components, error) {
	return f.internalClient.GenerateProvider(ctx, provider, providerType, options)
}

func (f fakeClient) GetClusterTemplate(ctx context.Context, options GetClusterTemplateOptions) (Template, error) {
	return f.internalClient.GetClusterTemplate(ctx, options)
}

func (f fakeClient) GetKubeconfig(ctx context.Context, options GetKubeconfigOptions) (string, error) {
	return f.internalClient.GetKubeconfig(ctx, options)
}

func (f fakeClient) Init(ctx context.Context, options InitOptions) ([]Components, error) {
	return f.internalClient.Init(ctx, options)
}

func (f fakeClient) InitImages(ctx context.Context, options InitOptions) ([]string, error) {
	return f.internalClient.InitImages(ctx, options)
}

func (f fakeClient) Delete(ctx context.Context, options DeleteOptions) error {
	return f.internalClient.Delete(ctx, options)
}

func (f fakeClient) Move(ctx context.Context, options MoveOptions) error {
	return f.internalClient.Move(ctx, options)
}

func (f fakeClient) PlanUpgrade(ctx context.Context, options PlanUpgradeOptions) ([]UpgradePlan, error) {
	return f.internalClient.PlanUpgrade(ctx, options)
}

func (f fakeClient) PlanCertManagerUpgrade(ctx context.Context, options PlanUpgradeOptions) (CertManagerUpgradePlan, error) {
	return f.internalClient.PlanCertManagerUpgrade(ctx, options)
}

func (f fakeClient) ApplyUpgrade(ctx context.Context, options ApplyUpgradeOptions) error {
	return f.internalClient.ApplyUpgrade(ctx, options)
}

func (f fakeClient) ProcessYAML(ctx context.Context, options ProcessYAMLOptions) (YamlPrinter, error) {
	return f.internalClient.ProcessYAML(ctx, options)
}

func (f fakeClient) RolloutRestart(ctx context.Context, options RolloutRestartOptions) error {
	return f.internalClient.RolloutRestart(ctx, options)
}

func (f fakeClient) DescribeCluster(ctx context.Context, options DescribeClusterOptions) (*tree.ObjectTree, error) {
	return f.internalClient.DescribeCluster(ctx, options)
}

func (f fakeClient) RolloutPause(ctx context.Context, options RolloutPauseOptions) error {
	return f.internalClient.RolloutPause(ctx, options)
}

func (f fakeClient) RolloutResume(ctx context.Context, options RolloutResumeOptions) error {
	return f.internalClient.RolloutResume(ctx, options)
}

// newFakeClient returns a clusterctl client that allows to execute tests on a set of fake config, fake repositories and fake clusters.
// you can use WithCluster and WithRepository to prepare for the test case.
func newFakeClient(ctx context.Context, configClient config.Client) *fakeClient {
	fake := &fakeClient{
		clusters:     map[cluster.Kubeconfig]cluster.Client{},
		repositories: map[string]repository.Client{},
	}

	fake.configClient = configClient
	if fake.configClient == nil {
		fake.configClient = newFakeConfig(ctx)
	}

	var clusterClientFactory = func(i ClusterClientFactoryInput) (cluster.Client, error) {
		// converting the client.Kubeconfig to cluster.Kubeconfig alias
		k := cluster.Kubeconfig(i.Kubeconfig)
		if _, ok := fake.clusters[k]; !ok {
			return nil, errors.Errorf("Cluster for kubeconfig %q and/or context %q does not exist", i.Kubeconfig.Path, i.Kubeconfig.Context)
		}
		return fake.clusters[k], nil
	}

	fake.internalClient, _ = newClusterctlClient(ctx, "fake-config",
		InjectConfig(fake.configClient),
		InjectClusterClientFactory(clusterClientFactory),
		InjectRepositoryFactory(func(_ context.Context, input RepositoryClientFactoryInput) (repository.Client, error) {
			if _, ok := fake.repositories[input.Provider.ManifestLabel()]; !ok {
				return nil, errors.Errorf("repository for kubeconfig %q does not exist", input.Provider.ManifestLabel())
			}
			return fake.repositories[input.Provider.ManifestLabel()], nil
		}),
		InjectCurrentContractVersion(currentContractVersion),
	)

	return fake
}

func (f *fakeClient) WithCluster(clusterClient cluster.Client) *fakeClient {
	input := clusterClient.Kubeconfig()
	f.clusters[input] = clusterClient
	return f
}

func (f *fakeClient) WithRepository(repositoryClient repository.Client) *fakeClient {
	if fc, ok := f.configClient.(fakeConfigClient); ok {
		fc.WithProvider(repositoryClient)
	}
	f.repositories[repositoryClient.ManifestLabel()] = repositoryClient
	return f
}

// newFakeCluster returns a fakeClusterClient that
// internally uses a FakeProxy (based on the controller-runtime FakeClient).
// You can use WithObjs to pre-load a set of runtime objects in the cluster.
func newFakeCluster(kubeconfig cluster.Kubeconfig, configClient config.Client) *fakeClusterClient {
	fake := &fakeClusterClient{
		kubeconfig:   kubeconfig,
		repositories: map[string]repository.Client{},
		certManager:  newFakeCertManagerClient(nil, nil),
	}

	fake.fakeProxy = test.NewFakeProxy()
	pollImmediateWaiter := func(context.Context, time.Duration, time.Duration, wait.ConditionWithContextFunc) error {
		return nil
	}

	fake.internalclient = cluster.New(kubeconfig, configClient,
		cluster.InjectProxy(fake.fakeProxy),
		cluster.InjectPollImmediateWaiter(pollImmediateWaiter),
		cluster.InjectRepositoryFactory(func(_ context.Context, provider config.Provider, _ config.Client, _ ...repository.Option) (repository.Client, error) {
			if _, ok := fake.repositories[provider.Name()]; !ok {
				return nil, errors.Errorf("repository for kubeconfig %q does not exist", provider.Name())
			}
			return fake.repositories[provider.Name()], nil
		}),
		cluster.InjectCurrentContractVersion(currentContractVersion),
		cluster.InjectGetCompatibleContractVersionsFunc(getCompatibleContractVersions),
	)
	return fake
}

// newFakeCertManagerClient creates a new CertManagerClient
// allows the caller to define which images are needed for the manager to run.
func newFakeCertManagerClient(imagesReturnImages []string, imagesReturnError error) *fakeCertManagerClient {
	return &fakeCertManagerClient{
		images:      imagesReturnImages,
		imagesError: imagesReturnError,
	}
}

type fakeCertManagerClient struct {
	images          []string
	imagesError     error
	certManagerPlan cluster.CertManagerUpgradePlan
}

var _ cluster.CertManagerClient = &fakeCertManagerClient{}

func (p *fakeCertManagerClient) EnsureInstalled(_ context.Context) error {
	return nil
}

func (p *fakeCertManagerClient) EnsureLatestVersion(_ context.Context) error {
	return nil
}

func (p *fakeCertManagerClient) PlanUpgrade(_ context.Context) (cluster.CertManagerUpgradePlan, error) {
	return p.certManagerPlan, nil
}

func (p *fakeCertManagerClient) Images(_ context.Context) ([]string, error) {
	return p.images, p.imagesError
}

func (p *fakeCertManagerClient) WithCertManagerPlan(plan CertManagerUpgradePlan) *fakeCertManagerClient {
	p.certManagerPlan = cluster.CertManagerUpgradePlan(plan)
	return p
}

type fakeClusterClient struct {
	kubeconfig      cluster.Kubeconfig
	fakeProxy       *test.FakeProxy
	fakeObjectMover cluster.ObjectMover
	repositories    map[string]repository.Client
	internalclient  cluster.Client
	certManager     cluster.CertManagerClient
}

var _ cluster.Client = &fakeClusterClient{}

func (f fakeClusterClient) Kubeconfig() cluster.Kubeconfig {
	return f.kubeconfig
}

func (f fakeClusterClient) Proxy() cluster.Proxy {
	return f.fakeProxy
}

func (f *fakeClusterClient) CertManager() cluster.CertManagerClient {
	return f.certManager
}

func (f fakeClusterClient) ProviderComponents() cluster.ComponentsClient {
	return f.internalclient.ProviderComponents()
}

func (f fakeClusterClient) ProviderInventory() cluster.InventoryClient {
	return f.internalclient.ProviderInventory()
}

func (f fakeClusterClient) ProviderInstaller() cluster.ProviderInstaller {
	return f.internalclient.ProviderInstaller()
}

func (f *fakeClusterClient) ObjectMover() cluster.ObjectMover {
	if f.fakeObjectMover == nil {
		return f.internalclient.ObjectMover()
	}
	return f.fakeObjectMover
}

func (f *fakeClusterClient) ProviderUpgrader() cluster.ProviderUpgrader {
	return f.internalclient.ProviderUpgrader()
}

func (f *fakeClusterClient) Template() cluster.TemplateClient {
	return f.internalclient.Template()
}

func (f *fakeClusterClient) WorkloadCluster() cluster.WorkloadCluster {
	return f.internalclient.WorkloadCluster()
}

func (f *fakeClusterClient) WithObjs(objs ...client.Object) *fakeClusterClient {
	f.fakeProxy.WithObjs(objs...)
	return f
}

func (f *fakeClusterClient) WithProviderInventory(name string, providerType clusterctlv1.ProviderType, version, targetNamespace string) *fakeClusterClient {
	f.fakeProxy.WithProviderInventory(name, providerType, version, targetNamespace)
	return f
}

func (f *fakeClusterClient) WithRepository(repositoryClient repository.Client) *fakeClusterClient {
	f.repositories[repositoryClient.Name()] = repositoryClient
	return f
}

func (f *fakeClusterClient) WithObjectMover(mover cluster.ObjectMover) *fakeClusterClient {
	f.fakeObjectMover = mover
	return f
}

func (f *fakeClusterClient) WithCertManagerClient(client cluster.CertManagerClient) *fakeClusterClient {
	f.certManager = client
	return f
}

// newFakeConfig return a fake implementation of the client for low-level config library.
// The implementation uses a FakeReader that stores configuration settings in a map; you can use
// the WithVar or WithProvider methods to set the map values.
func newFakeConfig(ctx context.Context) *fakeConfigClient {
	fakeReader := test.NewFakeReader()

	client, _ := config.New(ctx, "fake-config", config.InjectReader(fakeReader))

	return &fakeConfigClient{
		fakeReader:     fakeReader,
		internalclient: client,
	}
}

type fakeConfigClient struct {
	fakeReader     *test.FakeReader
	internalclient config.Client
}

var _ config.Client = &fakeConfigClient{}

func (f fakeConfigClient) CertManager() config.CertManagerClient {
	return f.internalclient.CertManager()
}

func (f fakeConfigClient) Providers() config.ProvidersClient {
	return f.internalclient.Providers()
}

func (f fakeConfigClient) Variables() config.VariablesClient {
	return f.internalclient.Variables()
}

func (f fakeConfigClient) ImageMeta() config.ImageMetaClient {
	return f.internalclient.ImageMeta()
}

func (f *fakeConfigClient) WithVar(key, value string) *fakeConfigClient {
	f.fakeReader.WithVar(key, value)
	return f
}

func (f *fakeConfigClient) WithProvider(provider config.Provider) *fakeConfigClient {
	f.fakeReader.WithProvider(provider.Name(), provider.Type(), provider.URL())
	return f
}

// newFakeRepository return a fake implementation of the client for low-level repository library.
// The implementation stores configuration settings in a map; you can use
// the WithPaths or WithDefaultVersion methods to configure the repository and WithFile to set the map values.
func newFakeRepository(ctx context.Context, provider config.Provider, configClient config.Client) *fakeRepositoryClient {
	fakeRepository := repository.NewMemoryRepository()

	if configClient == nil {
		configClient = newFakeConfig(ctx)
	}

	return &fakeRepositoryClient{
		Provider:       provider,
		configClient:   configClient,
		fakeRepository: fakeRepository,
		processor:      yaml.NewSimpleProcessor(),
	}
}

type fakeRepositoryClient struct {
	config.Provider
	configClient   config.Client
	fakeRepository *repository.MemoryRepository
	processor      yaml.Processor
}

var _ repository.Client = &fakeRepositoryClient{}

func (f fakeRepositoryClient) DefaultVersion() string {
	return f.fakeRepository.DefaultVersion()
}

func (f fakeRepositoryClient) GetVersions(ctx context.Context) ([]string, error) {
	return f.fakeRepository.GetVersions(ctx)
}

func (f fakeRepositoryClient) Components() repository.ComponentsClient {
	// use a fakeComponentClient (instead of the internal client used in other fake objects) we can de deterministic on what is returned (e.g. avoid interferences from overrides)
	return &fakeComponentClient{
		provider:       f.Provider,
		fakeRepository: f.fakeRepository,
		configClient:   f.configClient,
		processor:      f.processor,
	}
}

func (f fakeRepositoryClient) Templates(version string) repository.TemplateClient {
	// Use a fakeTemplateClient (instead of the internal client used in other fake objects) we can be deterministic on what is returned (e.g. avoid interferences from overrides)
	return &fakeTemplateClient{
		version:               version,
		fakeRepository:        f.fakeRepository,
		configVariablesClient: f.configClient.Variables(),
		processor:             f.processor,
	}
}

func (f fakeRepositoryClient) ClusterClasses(version string) repository.ClusterClassClient {
	// Use a fakeTemplateClient (instead of the internal client used in other fake objects) we can be deterministic on what is returned (e.g. avoid interferences from overrides)
	return &fakeClusterClassClient{
		version:               version,
		fakeRepository:        f.fakeRepository,
		configVariablesClient: f.configClient.Variables(),
		processor:             f.processor,
	}
}

func (f fakeRepositoryClient) Metadata(version string) repository.MetadataClient {
	// Use a fakeMetadataClient (instead of the internal client used in other fake objects) we can be deterministic on what is returned (e.g. avoid interferences from overrides)
	return &fakeMetadataClient{
		version:        version,
		fakeRepository: f.fakeRepository,
	}
}

func (f *fakeRepositoryClient) WithPaths(rootPath, componentsPath string) *fakeRepositoryClient {
	f.fakeRepository.WithPaths(rootPath, componentsPath)
	return f
}

func (f *fakeRepositoryClient) WithDefaultVersion(version string) *fakeRepositoryClient {
	f.fakeRepository.WithDefaultVersion(version)
	return f
}

func (f *fakeRepositoryClient) WithVersions(version ...string) *fakeRepositoryClient {
	f.fakeRepository.WithVersions(version...)
	return f
}

func (f *fakeRepositoryClient) WithMetadata(version string, metadata *clusterctlv1.Metadata) *fakeRepositoryClient {
	f.fakeRepository.WithMetadata(version, metadata)
	return f
}

func (f *fakeRepositoryClient) WithFile(version, path string, content []byte) *fakeRepositoryClient {
	f.fakeRepository.WithFile(version, path, content)
	return f
}

// fakeTemplateClient provides a super simple TemplateClient (e.g. without support for local overrides).
type fakeTemplateClient struct {
	version               string
	fakeRepository        *repository.MemoryRepository
	configVariablesClient config.VariablesClient
	processor             yaml.Processor
}

func (f *fakeTemplateClient) Get(ctx context.Context, flavor, targetNamespace string, skipTemplateProcess bool) (repository.Template, error) {
	name := "cluster-template"
	if flavor != "" {
		name = fmt.Sprintf("%s-%s", name, flavor)
	}
	name = fmt.Sprintf("%s.yaml", name)

	content, err := f.fakeRepository.GetFile(ctx, f.version, name)
	if err != nil {
		return nil, err
	}
	return repository.NewTemplate(repository.TemplateInput{
		RawArtifact:           content,
		ConfigVariablesClient: f.configVariablesClient,
		Processor:             f.processor,
		TargetNamespace:       targetNamespace,
		SkipTemplateProcess:   skipTemplateProcess,
	})
}

// fakeClusterClassClient provides a super simple TemplateClient (e.g. without support for local overrides).
type fakeClusterClassClient struct {
	version               string
	fakeRepository        *repository.MemoryRepository
	configVariablesClient config.VariablesClient
	processor             yaml.Processor
}

func (f *fakeClusterClassClient) Get(ctx context.Context, class, targetNamespace string, skipTemplateProcess bool) (repository.Template, error) {
	name := fmt.Sprintf("clusterclass-%s.yaml", class)
	content, err := f.fakeRepository.GetFile(ctx, f.version, name)
	if err != nil {
		return nil, err
	}
	return repository.NewTemplate(repository.TemplateInput{
		RawArtifact:           content,
		ConfigVariablesClient: f.configVariablesClient,
		Processor:             f.processor,
		TargetNamespace:       targetNamespace,
		SkipTemplateProcess:   skipTemplateProcess,
	})
}

// fakeMetadataClient provides a super simple MetadataClient (e.g. without support for local overrides/embedded metadata).
type fakeMetadataClient struct {
	version        string
	fakeRepository *repository.MemoryRepository
}

func (f *fakeMetadataClient) Get(ctx context.Context) (*clusterctlv1.Metadata, error) {
	content, err := f.fakeRepository.GetFile(ctx, f.version, "metadata.yaml")
	if err != nil {
		return nil, err
	}
	obj := &clusterctlv1.Metadata{}
	codecFactory := serializer.NewCodecFactory(scheme.Scheme)

	if err := runtime.DecodeInto(codecFactory.UniversalDecoder(), content, obj); err != nil {
		return nil, errors.Wrap(err, "error decoding metadata.yaml")
	}

	return obj, nil
}

// fakeComponentClient provides a super simple ComponentClient (e.g. without support for local overrides).
type fakeComponentClient struct {
	provider       config.Provider
	fakeRepository *repository.MemoryRepository
	configClient   config.Client
	processor      yaml.Processor
}

func (f *fakeComponentClient) Raw(ctx context.Context, options repository.ComponentsOptions) ([]byte, error) {
	return f.getRawBytes(ctx, &options)
}

func (f *fakeComponentClient) Get(ctx context.Context, options repository.ComponentsOptions) (repository.Components, error) {
	content, err := f.getRawBytes(ctx, &options)
	if err != nil {
		return nil, err
	}

	return repository.NewComponents(
		repository.ComponentsInput{
			Provider:     f.provider,
			ConfigClient: f.configClient,
			Processor:    f.processor,
			RawYaml:      content,
			Options:      options,
		},
	)
}

func (f *fakeComponentClient) getRawBytes(ctx context.Context, options *repository.ComponentsOptions) ([]byte, error) {
	if options.Version == "" {
		options.Version = f.fakeRepository.DefaultVersion()
	}
	path := f.fakeRepository.ComponentsPath()

	return f.fakeRepository.GetFile(ctx, options.Version, path)
}

func fakeCAPISetupObjects() []client.Object {
	return []client.Object{
		&apiextensionsv1.CustomResourceDefinition{
			ObjectMeta: metav1.ObjectMeta{Name: "clusters.cluster.x-k8s.io"},
			Spec: apiextensionsv1.CustomResourceDefinitionSpec{
				Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
					{
						Name:    currentContractVersion,
						Storage: true,
					},
				},
			},
		},
	}
}
