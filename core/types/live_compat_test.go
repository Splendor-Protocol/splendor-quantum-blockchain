package types

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveChainBlocksValidate re-runs the compat check against the live public
// tiers: block 0, 1 and 2, a block mid-chain, one a hundred below the head, and
// the head, from each of devnet, testnet and mainnet, read with plain GETs.
// The RPC URLs and chain ids come from the SDK's network profiles.
//
// Opt-in, so CI never depends on the network:
//
//	SQC_LIVE_COMPAT=1 go test ./core/types -run TestLiveChainBlocksValidate -v
//
// Run on a tree with #91's header encoding, every block fails with "block hash
// mismatch"; that is the regression the fixture test pins offline.
func TestLiveChainBlocksValidate(t *testing.T) {
	if os.Getenv("SQC_LIVE_COMPAT") == "" {
		t.Skip("set SQC_LIVE_COMPAT=1 to read live blocks from the public RPC tiers")
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "sdk", "sqc-sdk", "src", "config", "sqc.networks.json"))
	if err != nil {
		t.Fatalf("read network profiles: %v", err)
	}
	var profiles struct {
		Networks map[string]struct {
			ChainID string   `json:"chainId"`
			RPCURLs []string `json:"rpcUrls"`
		} `json:"networks"`
	}
	if err := json.Unmarshal(raw, &profiles); err != nil {
		t.Fatalf("parse network profiles: %v", err)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	get := func(url string, out any) error {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "sqc-compat-check/1")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(out)
	}

	for _, tier := range []string{"devnet", "testnet", "mainnet"} {
		p, ok := profiles.Networks[tier]
		if !ok || len(p.RPCURLs) == 0 {
			t.Fatalf("no %s profile", tier)
		}
		rpc := p.RPCURLs[0]
		t.Run(tier, func(t *testing.T) {
			var head Block
			if err := get(rpc+"/block/latest", &head); err != nil {
				t.Fatalf("head: %v", err)
			}
			top := head.Header.Height
			heights := []uint64{0, 1, 2, top / 2}
			if top > 100 {
				heights = append(heights, top-100)
			}
			for _, h := range heights {
				var b Block
				if err := get(fmt.Sprintf("%s/block/%d", rpc, h), &b); err != nil {
					t.Fatalf("block %d: %v", h, err)
				}
				checkLiveBlock(t, p.ChainID, &b)
			}
			checkLiveBlock(t, p.ChainID, &head)
		})
	}
}

func checkLiveBlock(t *testing.T, chainID string, b *Block) {
	t.Helper()
	if b.Header.ChainID != chainID {
		t.Errorf("height %d: chain id %q, profile says %q", b.Header.Height, b.Header.ChainID, chainID)
	}
	err := b.Validate()
	if err == nil {
		err = b.VerifySignature(b.ValidatorPubKey)
	}
	for _, v := range b.Votes {
		if err != nil {
			break
		}
		err = v.Verify(b.ValidatorPubKey)
	}
	t.Logf("%s height=%d hash=%x votes=%d txs=%d: %v",
		chainID, b.Header.Height, b.Hash, len(b.Votes), len(b.Transactions), err)
	if err != nil {
		t.Errorf("height %d does not validate: %v", b.Header.Height, err)
	}
}
