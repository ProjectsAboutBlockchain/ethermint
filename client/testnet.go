package client

// DONTCOVER

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"

	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/spf13/cobra"
	tmconfig "github.com/tendermint/tendermint/config"
	tmcrypto "github.com/tendermint/tendermint/crypto"
	tmos "github.com/tendermint/tendermint/libs/os"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	tmtypes "github.com/tendermint/tendermint/types"
	tmtime "github.com/tendermint/tendermint/types/time"

	"github.com/cosmos/cosmos-sdk/client/flags"
	clientkeys "github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys"
	"github.com/cosmos/cosmos-sdk/server"
	srvconfig "github.com/cosmos/cosmos-sdk/server/config"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authexported "github.com/cosmos/cosmos-sdk/x/auth/exported"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/crisis"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/cosmos/cosmos-sdk/x/mint"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/cosmos/ethermint/crypto"
	"github.com/cosmos/ethermint/types"
)

var (
	flagNodeDirPrefix     = "node-dir-prefix"
	flagNumValidators     = "v"
	flagOutputDir         = "output-dir"
	flagNodeDaemonHome    = "node-daemon-home"
	flagNodeCLIHome       = "node-cli-home"
	flagStartingIPAddress = "starting-ip-address"
)

const nodeDirPerm = 0755

// TestnetCmd initializes all files for tendermint testnet and application
func TestnetCmd(ctx *server.Context, cdc *codec.Codec,
	mbm module.BasicManager, genAccIterator authtypes.GenesisAccountIterator,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "testnet",
		Short: "Initialize files for a Ethermint testnet",
		Long: `testnet will create "v" number of directories and populate each with
necessary files (private validator, genesis, config, etc.).

Note, strict routability for addresses is turned off in the config file.`,

		Example: "ethermintd testnet --v 4 --keyring-backend test --output-dir ./output --starting-ip-address 192.168.10.2",
		RunE: func(cmd *cobra.Command, _ []string) error {
			config := ctx.Config

			outputDir, _ := cmd.Flags().GetString(flagOutputDir)
			keyringBackend, _ := cmd.Flags().GetString(flags.FlagKeyringBackend)
			chainID, _ := cmd.Flags().GetString(flags.FlagChainID)
			minGasPrices, _ := cmd.Flags().GetString(server.FlagMinGasPrices)
			nodeDirPrefix, _ := cmd.Flags().GetString(flagNodeDirPrefix)
			nodeDaemonHome, _ := cmd.Flags().GetString(flagNodeDaemonHome)
			nodeCLIHome, _ := cmd.Flags().GetString(flagNodeCLIHome)
			startingIPAddress, _ := cmd.Flags().GetString(flagStartingIPAddress)
			numValidators, _ := cmd.Flags().GetInt(flagNumValidators)

			return InitTestnet(
				cmd, config, cdc, mbm, genAccIterator, outputDir, chainID, minGasPrices,
				nodeDirPrefix, nodeDaemonHome, nodeCLIHome, startingIPAddress, keyringBackend, numValidators,
			)
		},
	}

	cmd.Flags().Int(flagNumValidators, 4, "Number of validators to initialize the testnet with")
	cmd.Flags().StringP(flagOutputDir, "o", "./build", "Directory to store initialization data for the testnet")
	cmd.Flags().String(flagNodeDirPrefix, "node", "Prefix the directory name for each node with (node results in node0, node1, ...)")
	cmd.Flags().String(flagNodeDaemonHome, "ethermintd", "Home directory of the node's daemon configuration")
	cmd.Flags().String(flagNodeCLIHome, "ethermintcli", "Home directory of the node's cli configuration")
	cmd.Flags().String(flagStartingIPAddress, "192.168.0.1", "Starting IP address (192.168.0.1 results in persistent peers list ID0@192.168.0.1:46656, ID1@192.168.0.2:46656, ...)")
	cmd.Flags().String(flags.FlagChainID, "", "genesis file chain-id, if left blank will be randomly created")
	cmd.Flags().String(server.FlagMinGasPrices, fmt.Sprintf("0.000006%s", types.DenomDefault), "Minimum gas prices to accept for transactions; All fees in a tx must meet this minimum (e.g. 0.01photon,0.001stake)")
	cmd.Flags().String(flags.FlagKeyringBackend, flags.DefaultKeyringBackend, "Select keyring's backend (os|file|test)")

	return cmd
}

// InitTestnet initializes the testnet configuration
func InitTestnet(
	cmd *cobra.Command, config *tmconfig.Config, cdc *codec.Codec,
	mbm module.BasicManager, genAccIterator authtypes.GenesisAccountIterator,
	outputDir, chainID, minGasPrices, nodeDirPrefix, nodeDaemonHome,
	nodeCLIHome, startingIPAddress, keyringBackend string, numValidators int,
) error {

	if chainID == "" {
		chainID = fmt.Sprintf("%d", tmrand.Int63())
	}

	nodeIDs := make([]string, numValidators)
	valPubKeys := make([]tmcrypto.PubKey, numValidators)

	simappConfig := srvconfig.DefaultConfig()
	simappConfig.MinGasPrices = minGasPrices

	var (
		genAccounts []authexported.GenesisAccount
		// genBalances []bank.Balance
		genFiles []string
	)

	inBuf := bufio.NewReader(cmd.InOrStdin())
	// generate private keys, node IDs, and initial transactions
	for i := 0; i < numValidators; i++ {
		nodeDirName := fmt.Sprintf("%s%d", nodeDirPrefix, i)
		nodeDir := filepath.Join(outputDir, nodeDirName, nodeDaemonHome)
		clientDir := filepath.Join(outputDir, nodeDirName, nodeCLIHome)
		gentxsDir := filepath.Join(outputDir, "gentxs")

		config.SetRoot(nodeDir)
		config.RPC.ListenAddress = "tcp://0.0.0.0:26657"

		if err := os.MkdirAll(filepath.Join(nodeDir, "config"), nodeDirPerm); err != nil {
			_ = os.RemoveAll(outputDir)
			return err
		}

		if err := os.MkdirAll(clientDir, nodeDirPerm); err != nil {
			_ = os.RemoveAll(outputDir)
			return err
		}

		config.Moniker = nodeDirName

		ip, err := getIP(i, startingIPAddress)
		if err != nil {
			_ = os.RemoveAll(outputDir)
			return err
		}

		nodeIDs[i], valPubKeys[i], err = genutil.InitializeNodeValidatorFiles(config)
		if err != nil {
			_ = os.RemoveAll(outputDir)
			return err
		}

		memo := fmt.Sprintf("%s@%s:26656", nodeIDs[i], ip)
		genFiles = append(genFiles, config.GenesisFile())

		kb, err := keys.NewKeyring(
			sdk.KeyringServiceName(),
			keyringBackend,
			clientDir,
			inBuf,
			crypto.EthSecp256k1Options()...,
		)
		if err != nil {
			return err
		}

		cmd.Printf(
			"Password for account '%s' :\n", nodeDirName,
		)

		keyPass := clientkeys.DefaultKeyPass
		addr, secret, err := server.GenerateSaveCoinKey(kb, nodeDirName, keyPass, true)
		if err != nil {
			_ = os.RemoveAll(outputDir)
			return err
		}

		info := map[string]string{"secret": secret}

		cliPrint, err := json.Marshal(info)
		if err != nil {
			return err
		}

		// save private key seed words
		if err := writeFile(fmt.Sprintf("%v.json", "key_seed"), clientDir, cliPrint); err != nil {
			return err
		}

		accTokens := sdk.TokensFromConsensusPower(1000)
		accStakingTokens := sdk.TokensFromConsensusPower(5000)
		coins := sdk.NewCoins(
			sdk.NewCoin(sdk.DefaultBondDenom, accTokens),
			sdk.NewCoin(types.DenomDefault, accStakingTokens),
		)

		// genBalances = append(genBalances, bank.Balance{Address: addr, Coins: coins})
		genAccounts = append(genAccounts, types.EthAccount{
			BaseAccount: authtypes.NewBaseAccount(addr, coins, nil, 0, 0),
			CodeHash:    ethcrypto.Keccak256(nil),
		})

		valTokens := sdk.TokensFromConsensusPower(100)
		msg := stakingtypes.NewMsgCreateValidator(
			sdk.ValAddress(addr),
			valPubKeys[i],
			sdk.NewCoin(types.DenomDefault, valTokens),
			stakingtypes.NewDescription(nodeDirName, "", "", "", ""),
			stakingtypes.NewCommissionRates(sdk.OneDec(), sdk.OneDec(), sdk.OneDec()),
			sdk.OneInt(),
		)

		tx := authtypes.NewStdTx([]sdk.Msg{msg}, authtypes.StdFee{}, []authtypes.StdSignature{}, memo) //nolint:staticcheck // SA1019: authtypes.StdFee is deprecated
		txBldr := authtypes.NewTxBuilderFromCLI(inBuf).WithChainID(chainID).WithMemo(memo).WithKeybase(kb)

		signedTx, err := txBldr.SignStdTx(nodeDirName, clientkeys.DefaultKeyPass, tx, false)
		if err != nil {
			_ = os.RemoveAll(outputDir)
			return err
		}

		txBytes, err := cdc.MarshalJSON(signedTx)
		if err != nil {
			_ = os.RemoveAll(outputDir)
			return err
		}

		// gather gentxs folder
		if err := writeFile(fmt.Sprintf("%v.json", nodeDirName), gentxsDir, txBytes); err != nil {
			_ = os.RemoveAll(outputDir)
			return err
		}

		srvconfig.WriteConfigFile(filepath.Join(nodeDir, "config/app.toml"), simappConfig)
	}

	if err := initGenFiles(cdc, mbm, chainID, genAccounts, genFiles, numValidators); err != nil {
		return err
	}

	err := collectGenFiles(
		cdc, config, chainID, nodeIDs, valPubKeys, numValidators,
		outputDir, nodeDirPrefix, nodeDaemonHome, genAccIterator,
	)
	if err != nil {
		return err
	}

	cmd.PrintErrf("Successfully initialized %d node directories\n", numValidators)
	return nil
}

func initGenFiles(
	cdc *codec.Codec, mbm module.BasicManager, chainID string,
	genAccounts []authexported.GenesisAccount,
	genFiles []string, numValidators int,
) error {

	appGenState := mbm.DefaultGenesis()

	// set the accounts in the genesis state
	var authGenState authtypes.GenesisState
	cdc.MustUnmarshalJSON(appGenState[authtypes.ModuleName], &authGenState)

	authGenState.Accounts = genAccounts
	appGenState[authtypes.ModuleName] = cdc.MustMarshalJSON(authGenState)

	// set the balances in the genesis state
	// var bankGenState bank.GenesisState
	// cdc.MustUnmarshalJSON(appGenState[bank.ModuleName], &bankGenState)

	// bankGenState.Balances = genBalances
	// appGenState[bank.ModuleName] = cdc.MustMarshalJSON(bankGenState)

	var stakingGenState stakingtypes.GenesisState
	cdc.MustUnmarshalJSON(appGenState[stakingtypes.ModuleName], &stakingGenState)

	stakingGenState.Params.BondDenom = types.DenomDefault
	appGenState[stakingtypes.ModuleName] = cdc.MustMarshalJSON(stakingGenState)

	var govGenState govtypes.GenesisState
	cdc.MustUnmarshalJSON(appGenState[govtypes.ModuleName], &govGenState)

	govGenState.DepositParams.MinDeposit[0].Denom = types.DenomDefault
	appGenState[govtypes.ModuleName] = cdc.MustMarshalJSON(govGenState)

	var mintGenState mint.GenesisState
	cdc.MustUnmarshalJSON(appGenState[mint.ModuleName], &mintGenState)

	mintGenState.Params.MintDenom = types.DenomDefault
	appGenState[mint.ModuleName] = cdc.MustMarshalJSON(mintGenState)

	var crisisGenState crisis.GenesisState
	cdc.MustUnmarshalJSON(appGenState[crisis.ModuleName], &crisisGenState)

	crisisGenState.ConstantFee.Denom = types.DenomDefault
	appGenState[crisis.ModuleName] = cdc.MustMarshalJSON(crisisGenState)

	appGenStateJSON, err := codec.MarshalJSONIndent(cdc, appGenState)
	if err != nil {
		return err
	}

	genDoc := tmtypes.GenesisDoc{
		ChainID:    chainID,
		AppState:   appGenStateJSON,
		Validators: nil,
	}

	// generate empty genesis files for each validator and save
	for i := 0; i < numValidators; i++ {
		if err := genDoc.SaveAs(genFiles[i]); err != nil {
			return err
		}
	}
	return nil
}

func collectGenFiles(
	cdc *codec.Codec, config *tmconfig.Config, chainID string,
	nodeIDs []string, valPubKeys []tmcrypto.PubKey,
	numValidators int, outputDir, nodeDirPrefix, nodeDaemonHome string,
	genAccIterator authtypes.GenesisAccountIterator,
) error {

	var appState json.RawMessage
	genTime := tmtime.Now()

	for i := 0; i < numValidators; i++ {
		nodeDirName := fmt.Sprintf("%s%d", nodeDirPrefix, i)
		nodeDir := filepath.Join(outputDir, nodeDirName, nodeDaemonHome)
		gentxsDir := filepath.Join(outputDir, "gentxs")
		config.Moniker = nodeDirName

		config.SetRoot(nodeDir)

		nodeID, valPubKey := nodeIDs[i], valPubKeys[i]
		initCfg := genutiltypes.NewInitConfig(chainID, gentxsDir, nodeID, nodeID, valPubKey)

		genDoc, err := tmtypes.GenesisDocFromFile(config.GenesisFile())
		if err != nil {
			return err
		}

		nodeAppState, err := genutil.GenAppStateFromConfig(cdc, config, initCfg, *genDoc, genAccIterator)
		if err != nil {
			return err
		}

		if appState == nil {
			// set the canonical application state (they should not differ)
			appState = nodeAppState
		}

		genFile := config.GenesisFile()

		// overwrite each validator's genesis file to have a canonical genesis time
		if err := genutil.ExportGenesisFileWithTime(genFile, chainID, nil, appState, genTime); err != nil {
			return err
		}
	}

	return nil
}

func getIP(i int, startingIPAddr string) (ip string, err error) {
	if len(startingIPAddr) == 0 {
		ip, err = server.ExternalIP()
		if err != nil {
			return "", err
		}
		return ip, nil
	}
	return calculateIP(startingIPAddr, i)
}

func calculateIP(ip string, i int) (string, error) {
	ipv4 := net.ParseIP(ip).To4()
	if ipv4 == nil {
		return "", fmt.Errorf("%v: non ipv4 address", ip)
	}

	for j := 0; j < i; j++ {
		ipv4[3]++
	}

	return ipv4.String(), nil
}

func writeFile(name string, dir string, contents []byte) error {
	writePath := filepath.Join(dir)
	file := filepath.Join(writePath, name)

	err := tmos.EnsureDir(writePath, 0755)
	if err != nil {
		return err
	}

	err = tmos.WriteFile(file, contents, 0644)
	if err != nil {
		return err
	}

	return nil
}
