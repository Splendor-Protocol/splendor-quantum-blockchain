# CI/CD Deployment Guide - Complete Chain Reset

**Treasury Address:** `SQ:665f89b60e3ebb95eac3ac6c6ed89f14caa74947d64bc9300cd4609f0075fc4c`  
**Total Supply:** 26 billion SPLD (ALL goes to treasury)  
**Validator:** NEW fresh wallet (signer only, gets 0 SPLD)

---

## Step 1: Generate NEW Validator Wallet

```bash
# Generate a completely fresh validator wallet
docker run --rm ghcr.io/splendor-protocol/sqc-node:latest sqc-wallet generate

# Save the output:
# - Private key seed (64 hex or 24 words) → STORE IN GITHUB SECRETS
# - Address: SQ:xxx... → Validator address (will have 0 balance)
# - Public key will be extracted in next step
```

**Save these to a SECURE location:**
- Seed phrase (24 words) or 64 hex seed
- Validator address

---

## Step 2: Extract Validator Public Key

```bash
# If you saved the seed as 24 words or 64 hex:
docker run --rm -i ghcr.io/splendor-protocol/sqc-node:latest sqc-wallet restore --seed "YOUR_24_WORDS_OR_64_HEX"

# This will output the public key hex (3904 hex characters)
# Copy this - you'll need it for genesis.json
```

---

## Step 3: Update GitHub Secrets

Go to your `splendor-infra` repository:

**Settings → Secrets and variables → Actions → New repository secret**

Add these secrets:

```
Name: VALIDATOR_SEED_PROD
Value: <your 24-word phrase OR 64 hex seed>

Name: VALIDATOR_PUBKEY_PROD  
Value: <3904 hex characters from step 2>

Name: TREASURY_ADDRESS
Value: SQ:665f89b60e3ebb95eac3ac6c6ed89f14caa74947d64bc9300cd4609f0075fc4c

Name: SQC_RPC_AUTH_TOKEN
Value: <generate with: openssl rand -hex 32>
```

---

## Step 4: Create Genesis Configuration

In `splendor-infra/.github/workflows/deploy.yml`, add this step:

```yaml
- name: Generate Production Genesis
  env:
    VALIDATOR_PUBKEY: ${{ secrets.VALIDATOR_PUBKEY_PROD }}
    TREASURY_ADDR: ${{ secrets.TREASURY_ADDRESS }}
  run: |
    cat > genesis.json <<'EOF'
    {
      "chain_id": "splendor-quantum-1",
      "network_name": "Splendor Quantum Chain Mainnet",
      "timestamp": $(date +%s),
      "validator_pubkey_hex": "$VALIDATOR_PUBKEY",
      "allocations": [
        {
          "address": "$TREASURY_ADDR",
          "balance": "26000000000000000000000000000",
          "comment": "Treasury - ALL 26 billion SPLD"
        }
      ],
      "total_supply": "26000000000000000000000000000",
      "forks": {
        "module_revert_v1": 1,
        "qrc20_v2": 1,
        "qrc1155_v1": 1,
        "exchange_v1": 1
      }
    }
    EOF
    
    # Validate genesis
    cat genesis.json | jq .
    
    # Ensure only ONE allocation to treasury
    ALLOC_COUNT=$(cat genesis.json | jq '.allocations | length')
    if [ "$ALLOC_COUNT" != "1" ]; then
      echo "ERROR: Must have exactly 1 allocation (treasury)"
      exit 1
    fi
    
    # Ensure treasury address is correct
    ADDR=$(cat genesis.json | jq -r '.allocations[0].address')
    if [ "$ADDR" != "$TREASURY_ADDR" ]; then
      echo "ERROR: Treasury address mismatch"
      exit 1
    fi
```

---

## Step 5: Update Docker Deployment Step

In the same workflow file, update the node deployment:

```yaml
- name: Deploy Validator Node
  env:
    VALIDATOR_SEED: ${{ secrets.VALIDATOR_SEED_PROD }}
    RPC_AUTH_TOKEN: ${{ secrets.SQC_RPC_AUTH_TOKEN }}
  run: |
    # Stop old node
    docker stop splendor-quantum-validator || true
    docker rm splendor-quantum-validator || true
    
    # Restore validator wallet from seed
    echo "$VALIDATOR_SEED" | docker run --rm -i \
      -v $(pwd)/validator.sqwallet:/validator.sqwallet \
      ghcr.io/splendor-protocol/sqc-node:latest \
      sqc-wallet restore --seed - --out /validator.sqwallet
    
    # Deploy new validator
    docker run -d \
      --name splendor-quantum-validator \
      --restart unless-stopped \
      -e SQC_WALLET_PASS="${{ secrets.SQC_WALLET_PASS }}" \
      -e SQC_RPC_AUTH_TOKEN="$RPC_AUTH_TOKEN" \
      -v $(pwd)/validator.sqwallet:/sqc/validator.sqwallet:ro \
      -v $(pwd)/genesis.json:/sqc/genesis.json:ro \
      -v /var/sqc/data:/sqc/data \
      -p 8080:8080 \
      ghcr.io/splendor-protocol/sqc-node:latest \
      --data-dir /sqc/data \
      --rpc-addr 0.0.0.0:8080 \
      --genesis /sqc/genesis.json \
      --validator \
      --wallet /sqc/validator.sqwallet \
      --block-interval 2 \
      --log-level info
```

---

## Step 6: Perform Complete Chain Reset

**BEFORE RUNNING:** Take an EBS snapshot if you want to preserve old chain data.

```bash
# From your local machine (with GitHub CLI installed)
INF=Splendor-Protocol/splendor-infra

# For PRODUCTION (mainnet)
gh workflow run "Deploy quantum infrastructure" -R $INF --ref main \
  -f environment=prod \
  -f rollout_node=true \
  -f apply_infrastructure=false \
  -f reset_chain=true \
  -f reset_confirm="reset prod chain"

# Watch the deployment
gh run watch
```

**What this does:**
1. Generates genesis.json with:
   - NEW validator public key
   - Treasury address: `SQ:665f89b60e3ebb95eac3ac6c6ed89f14caa74947d64bc9300cd4609f0075fc4c`
   - 26 billion SPLD → treasury ONLY
2. Restores validator wallet from seed
3. Wipes old chain data
4. Starts fresh chain from block 0
5. Validator signs blocks (gets 0 SPLD)
6. Treasury has all 26B SPLD

---

## Step 7: Verify Deployment

```bash
# Check node is running
curl https://quantum-rpc.splendor.org/healthz

# Check genesis block
curl https://quantum-rpc.splendor.org/block/0 | jq .

# ✅ CRITICAL: Verify treasury has 26B SPLD
curl "https://quantum-rpc.splendor.org/balance/SQ:665f89b60e3ebb95eac3ac6c6ed89f14caa74947d64bc9300cd4609f0075fc4c" | jq .

# Expected output:
# {
#   "balance": "26000000000000000000000000000",
#   "nonce": 0
# }

# ✅ CRITICAL: Verify validator has 0 SPLD
VALIDATOR_ADDR=$(curl -s https://quantum-rpc.splendor.org/block/0 | jq -r '.header.validator')
curl "https://quantum-rpc.splendor.org/balance/$VALIDATOR_ADDR" | jq .

# Expected output:
# {
#   "balance": "0",
#   "nonce": 0
# }

# Check blocks are being produced
watch -n 2 "curl -s https://quantum-rpc.splendor.org/block/latest | jq '.header.height'"
```

---

## Step 8: Post-Deployment Tasks

After deployment:

1. **Update Explorer**
   - Redeploy block explorer (it caches genesis + blocks)
   - Update `--genesis-doc-hash` if pinned

2. **Notify Ecosystem**
   - New genesis hash
   - New validator address
   - Chain restarted at height 0

3. **Re-deploy Contracts**
   - All contracts are gone
   - QRC20 tokens must be redeployed
   - Update contract addresses in frontend

---

## Complete Genesis File (For Reference)

```json
{
  "chain_id": "splendor-quantum-1",
  "network_name": "Splendor Quantum Chain Mainnet",
  "timestamp": 1741545600,
  "validator_pubkey_hex": "<NEW_VALIDATOR_PUBKEY_3904_HEX>",
  "allocations": [
    {
      "address": "SQ:665f89b60e3ebb95eac3ac6c6ed89f14caa74947d64bc9300cd4609f0075fc4c",
      "balance": "26000000000000000000000000000",
      "comment": "Treasury - ALL 26 billion SPLD"
    }
  ],
  "total_supply": "26000000000000000000000000000",
  "forks": {
    "module_revert_v1": 1,
    "qrc20_v2": 1,
    "qrc1155_v1": 1,
    "exchange_v1": 1
  }
}
```

---

## Security Checklist

- [ ] New validator seed stored in GitHub Secrets (never committed to git)
- [ ] Only ONE allocation in genesis (treasury address)
- [ ] Validator address != treasury address
- [ ] Total supply = 26,000,000,000 SPLD
- [ ] RPC auth token rotated
- [ ] Old validator wallet backed up (if needed) then deleted
- [ ] Genesis hash pinned on all full nodes
- [ ] Firewall rules updated for new validator

---

## Troubleshooting

**Issue: "genesis validation failed"**
```bash
# Check the generated genesis.json
gh run download <run-id> -n genesis

# Validate locally
docker run --rm -v $(pwd)/genesis.json:/genesis.json \
  ghcr.io/splendor-protocol/sqc-node:latest \
  sqc-node validate-genesis /genesis.json
```

**Issue: "validator not producing blocks"**
```bash
# Check validator logs
gh run view --log

# Ensure wallet password is set in secrets
gh secret set SQC_WALLET_PASS --body "your-password"
```

**Issue: "treasury balance is 0"**
```bash
# Genesis was not applied - check that --genesis flag is set
# Verify genesis.json was mounted correctly in the container
docker exec splendor-quantum-validator cat /sqc/genesis.json
```

---

## Summary

✅ **Treasury Address:** `SQ:665f89b60e3ebb95eac3ac6c6ed89f14caa74947d64bc9300cd4609f0075fc4c`  
✅ **Supply:** 26 billion SPLD (all to treasury)  
✅ **Validator:** NEW fresh wallet (signer only, 0 balance)  
✅ **Deployment:** Automated via GitHub Actions CI/CD  
✅ **Chain:** Complete reset from block 0

Run the workflow and verify with the commands in Step 7!
