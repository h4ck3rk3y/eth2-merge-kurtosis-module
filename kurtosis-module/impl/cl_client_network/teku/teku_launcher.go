package teku

import (
	"fmt"
	"github.com/kurtosis-tech/eth2-merge-kurtosis-module/kurtosis-module/impl/cl_client_network"
	"github.com/kurtosis-tech/eth2-merge-kurtosis-module/kurtosis-module/impl/cl_client_network/cl_client_rest_client"
	"github.com/kurtosis-tech/eth2-merge-kurtosis-module/kurtosis-module/impl/service_launch_utils"
	"github.com/kurtosis-tech/kurtosis-core-api-lib/api/golang/lib/enclaves"
	"github.com/kurtosis-tech/kurtosis-core-api-lib/api/golang/lib/services"
	"github.com/kurtosis-tech/stacktrace"
	"strings"
	"time"
)

const (
	imageName = "consensys/teku:latest"

	consensusDataDirpathOnServiceContainer = "/consensus-data"

	// Port IDs
	tcpDiscoveryPortID = "tcp-discovery"
	udpDiscoveryPortID = "udp-discovery"
	httpPortID         = "http"

	// Port nums
	discoveryPortNum uint16 = 9000
	httpPortNum             = 4000

	// To start a bootnode rather than a child node, we provide this string to the launchNode function
	bootnodeEnrStrForStartingBootnode = ""

	genesisConfigYmlRelFilepathInSharedDir = "genesis-config.yml"
	genesisSszRelFilepathInSharedDir = "genesis.ssz"

	maxNumHealthcheckRetries = 10
	timeBetweenHealthcheckRetries = 1 * time.Second
)
var usedPorts = map[string]*services.PortSpec{
	// TODO Add metrics port
	tcpDiscoveryPortID: services.NewPortSpec(discoveryPortNum, services.PortProtocol_TCP),
	udpDiscoveryPortID: services.NewPortSpec(discoveryPortNum, services.PortProtocol_UDP),
	httpPortID:         services.NewPortSpec(httpPortNum, services.PortProtocol_TCP),
}

type TekuCLClientLauncher struct {

}

func (t TekuCLClientLauncher) LaunchBootNode(enclaveCtx *enclaves.EnclaveContext, serviceId services.ServiceID, elClientRpcSockets map[string]bool, totalTerminalDifficulty uint32) (resultClientCtx *cl_client_network.ConsensusLayerClientContext, resultErr error) {
	panic("implement me")
}

func (t TekuCLClientLauncher) LaunchChildNode(enclaveCtx *enclaves.EnclaveContext, serviceId services.ServiceID, bootnodeEnr string, elClientRpcSockets map[string]bool, totalTerminalDifficulty uint32) (resultClientCtx *cl_client_network.ConsensusLayerClientContext, resultErr error) {
	panic("implement me")
}

// ====================================================================================================
//                                   Private Helper Methods
// ====================================================================================================
func (launcher *TekuCLClientLauncher) launchNode(
	enclaveCtx *enclaves.EnclaveContext,
	serviceId services.ServiceID,
	bootnodeEnr string,
	elClientRpcSockets map[string]bool,
	genesisConfigYmlFilepathOnModuleContainer string,
	genesisSzzFilepathOnModuleContainer string,
	totalTerminalDiffulty uint32,
) (
	resultClientCtx *cl_client_network.ConsensusLayerClientContext,
	resultErr error,
) {
	containerConfigSupplier := launcher.getContainerConfigSupplier(
		bootnodeEnr,
		elClientRpcSockets,
		genesisConfigYmlFilepathOnModuleContainer,
		genesisSzzFilepathOnModuleContainer,
		totalTerminalDiffulty,
	)
	serviceCtx, err := enclaveCtx.AddService(serviceId, containerConfigSupplier)
	if err != nil {
		return nil, stacktrace.Propagate(err, "An error occurred launching the Lighthouse CL client with service ID '%v'", serviceId)
	}

	httpPort, found := serviceCtx.GetPrivatePorts()[httpPortID]
	if !found {
		return nil, stacktrace.NewError("Expected new Lighthouse service to have port with ID '%v', but none was found", httpPortID)
	}

	restClient := cl_client_rest_client.NewCLClientRESTClient(serviceCtx.GetPrivateIPAddress(), httpPort.GetNumber())

	if err := waitForAvailability(restClient); err != nil {
		return nil, stacktrace.Propagate(err, "An error occurred waiting for the new Lighthouse node to become available")
	}

	nodeIdentity, err := restClient.GetNodeIdentity()
	if err != nil {
		return nil, stacktrace.Propagate(err, "An error occurred getting the new Lighthouse node's identity, which is necessary to retrieve its ENR")
	}

	result := cl_client_network.NewConsensusLayerClientContext(
		serviceCtx,
		nodeIdentity.ENR,
		httpPortID,
	)

	return result, nil
}

func (launcher *TekuCLClientLauncher) getContainerConfigSupplier(
	bootNodeEnr string,
	elClientRpcSockets map[string]bool,
	genesisConfigYmlFilepathOnModuleContainer string,
	genesisSszFilepathOnModuleContainer string,
	totalTerminalDiffulty uint32,
) func(string, *services.SharedPath) (*services.ContainerConfig, error) {
	containerConfigSupplier := func(privateIpAddr string, sharedDir *services.SharedPath) (*services.ContainerConfig, error) {
		genesisConfigYmlSharedPath := sharedDir.GetChildPath(genesisConfigYmlRelFilepathInSharedDir)
		if err := service_launch_utils.CopyFileToSharedPath(genesisConfigYmlFilepathOnModuleContainer, genesisConfigYmlSharedPath); err != nil {
			return nil, stacktrace.Propagate(
				err,
				"An error occurred copying the genesis config YML from '%v' to shared dir relative path '%v'",
				genesisConfigYmlFilepathOnModuleContainer,
				genesisConfigYmlRelFilepathInSharedDir,
			)
		}

		genesisSszSharedPath := sharedDir.GetChildPath(genesisSszRelFilepathInSharedDir)
		if err := service_launch_utils.CopyFileToSharedPath(genesisSszFilepathOnModuleContainer, genesisSszSharedPath); err != nil {
			return nil, stacktrace.Propagate(
				err,
				"An error occurred copying the genesis SSZ from '%v' to shared dir relative path '%v'",
				genesisSszFilepathOnModuleContainer,
				genesisSszRelFilepathInSharedDir,
			)
		}

		elClientRpcUrls := []string{}
		for rpcSocketStr := range elClientRpcSockets {
			rpcUrlStr := fmt.Sprintf("http://%v", rpcSocketStr)
			elClientRpcUrls = append(elClientRpcUrls, rpcUrlStr)
		}
		elClientRpcUrlsStr := strings.Join(elClientRpcUrls, ",")

		cmdArgs := []string{
			"--network=" + genesisConfigYmlSharedPath.GetAbsPathOnServiceContainer(),
			"--initial-state=" + genesisSszSharedPath.GetAbsPathOnServiceContainer(),
			"--data-path=" + consensusDataDirpathOnServiceContainer,
			"--data-storage-mode=PRUNE",
			"--p2p-enabled=true",
			"--eth1-endpoints=" + elClientRpcUrlsStr,
			"--Xee-endpoint=" + elClientRpcUrlsStr,
			"--p2p-advertised-ip=" + privateIpAddr,
			"--rest-api-enabled=true",
			"--rest-api-docs-enabled=true",
			"--rest-api-interface=0.0.0.0",
			fmt.Sprintf("--rest-api-port=%v", httpPortNum),
			"--rest-api-host-allowlist=*",
			"--Xdata-storage-non-canonical-blocks-enabled=true",
			fmt.Sprintf("--Xnetwork-merge-total-terminal-difficulty=%v", totalTerminalDiffulty),
			"--log-destination=CONSOLE",
		}
		if bootNodeEnr != bootnodeEnrStrForStartingBootnode {
			cmdArgs = append(cmdArgs, "--p2p-discovery-bootnodes" + bootNodeEnr)
		}

		containerConfig := services.NewContainerConfigBuilder(
			imageName,
		).WithUsedPorts(
			usedPorts,
		).WithCmdOverride(
			cmdArgs,
		).Build()

		return containerConfig, nil
	}
	return containerConfigSupplier
}

func waitForAvailability(restClient *cl_client_rest_client.CLClientRESTClient) error {
	for i := 0; i < maxNumHealthcheckRetries; i++ {
		_, err := restClient.GetHealth()
		if err == nil {
			// TODO check the healthstatus???
			return nil
		}
		time.Sleep(timeBetweenHealthcheckRetries)
	}
	return stacktrace.NewError(
		"Lighthouse node didn't become available even after %v retries with %v between retries",
		maxNumHealthcheckRetries,
		timeBetweenHealthcheckRetries,
	)
}