# Bridge relayer on AWS — hardened deployment

The relayer watches legacy chain 2691 for `Burned` events and submits `bridge.claim`
attestations on the quantum testnet. In 1-of-1 it is **the sole attestor and the claim
submitter**, so its wallet is the one key that, if stolen, could forge claims (bounded by
`max_claim_amount` and the escrow pool, and only on a **testnet** here). Treat it as custody.

## ⚠️ KMS cannot hold this key — and why that's OK

AWS KMS asymmetric keys are RSA / ECC / SM2 only. The attestor key is **ML-DSA-65
(post-quantum)** — KMS cannot store or sign with it. So "KMS for the key" is not literally
possible. The equivalent-safety pattern:

- The operator **`.sqwallet`** (already AES-encrypted under `SQC_WALLET_PASS`) and the
  passphrase live in **AWS Secrets Manager** (two secrets), each encrypted at rest by a
  **KMS CMK** — so KMS still guards them, one layer out.
- At container start an entrypoint pulls both from Secrets Manager into a **tmpfs** mount
  (memory only, never persistent disk), the binary loads the wallet, and the plaintext is
  wiped. Nothing sensitive touches EBS/EFS or the image.

## Topology (Fargate — no host to manage)

```
ECS Fargate task (awsvpc, private subnet, no public IP)
  ├─ container: splendor/bridge-relayer:testnet  (distroless, nonroot)
  ├─ task role: secretsmanager:GetSecretValue on ONLY the 2 relayer secrets (+ kms:Decrypt on their CMK)
  ├─ tmpfs mount for the wallet;  EFS mount at /var/sqc for the state file
  ├─ egress: 443 to mainnet-rpc.splendor.org + quantum-rpc-testnet.splendor.org only (SG/NACL)
  └─ NO inbound (the relayer opens no ports)
```

EC2 alternative: a minimal Amazon Linux instance in a private subnet, the same container under
systemd, IMDSv2-only, SSM-only access (no SSH). Fargate is preferred — fewer moving parts, no
patching.

## Secrets (create once)

```sh
aws secretsmanager create-secret --name splendor/bridge-relayer/testnet/wallet   --secret-binary  fileb://operator.sqwallet
aws secretsmanager create-secret --name splendor/bridge-relayer/testnet/passphrase --secret-string '<SQC_WALLET_PASS>'
```

## Run config (matches the bring-up runbook Step 5)

The task's entrypoint fetches the secrets to tmpfs, then execs:

```
bridge-relayer \
  --evm-rpc https://mainnet-rpc.splendor.org/ --evm-chain-id 2691 \
  --evm-contract 0x<BURN_ADDR> --start-block <BURN_DEPLOY_BLOCK> \
  --node https://quantum-rpc-testnet.splendor.org --chain-id splendor-quantum-testnet-1 \
  --bridge SQ:<INSTANCE> --wallet /run/wallet/operator.sqwallet \
  --state /var/sqc/bridge-relayer-testnet.json --confirmations 120
```
`SQC_WALLET_PASS` is set from the passphrase secret. 1-of-1 ⇒ no `--attestation-dir`.

## Durability

State loss is **recoverable, not catastrophic**: claim idempotency lives on-chain (the claimed
slot), so a fresh task with `--start-block <BURN_DEPLOY_BLOCK>` re-scans and skips already-credited
burns. EFS just avoids a full re-scan. Run **one** task (no parallel relayers on the same cursor).

## Health

CloudWatch Logs from the container; alarm on the relayer's `bridge paused` / `hold` log lines
(cursor not advancing) and on task restarts. A healthy relayer logs each claim submitted + credited.
