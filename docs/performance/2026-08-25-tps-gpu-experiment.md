# TPS and GPU Verification Experiment

**Date:** 2026-08-25

**Branch:** `hardening`

**Baseline commit:** `b2e5214`

**Scope:** ML-DSA-65 verification and current block-production throughput

## Summary

GPU acceleration is technically feasible. NVIDIA cuPQC 0.4.1 successfully
verified an actual Splendor transaction vector produced by CIRCL, reaching about
2.07 million ML-DSA-65 verifications per second on an NVIDIA A10G at a batch
size of 10,000.

That number is not end-to-end chain TPS. The current block-production path
processed about 2,605 transactions per second on the same AWS host. Profiling
showed that transaction JSON hashing/base64 encoding, repeated public-key
decoding, serial state execution, receipt construction, Merkle work, and
storage remain substantial costs. Replacing only signature verification with a
GPU is therefore expected to produce a modest whole-pipeline improvement, not
million-TPS blocks.

The best first step is CPU-parallel stateless prevalidation plus cached hashes
and decoded public keys. A GPU batch verifier becomes useful after those
bottlenecks are removed and the node consistently has large transaction
batches.

## Test environments

### Local baseline

- Apple M2
- CIRCL ML-DSA-65 implementation used by the chain
- 10,000-transaction block production: approximately 3.15-3.32 seconds
  (3,011-3,171 TPS with debug logging disabled)

### AWS GPU test

- Region: `us-east-1`
- Instance: `g5.xlarge`
- CPU: 4 vCPU AMD EPYC 7R32, 2.8 GHz
- GPU: NVIDIA A10G, 23,028 MiB
- AMI: AWS Deep Learning Base OSS NVIDIA Driver GPU AMI, Ubuntu 24.04,
  `ami-0a30b02f6c5660457`
- CUDA: 13.2
- NVIDIA driver: 595.91.07
- NVIDIA cuPQC: 0.4.1

A `g6.xlarge` was attempted first, but AWS returned insufficient capacity in
all tested `us-east-1` availability zones. The `g5.xlarge` was a suitable
fallback. The account quota also constrained the experiment to four additional
GPU-family vCPUs.

## Results

| Measurement | Result |
|---|---:|
| Current block pipeline, 1,000 transactions | 2,901 TPS |
| Current block pipeline, 5,000 transactions | 2,790 TPS |
| Current block pipeline, 10,000 transactions | 2,605 TPS |
| CPU ML-DSA verify, one worker | 28,569/s |
| CPU ML-DSA verify, two workers | 56,191/s |
| CPU ML-DSA verify, four workers | 81,000/s |
| CPU ML-DSA verify, eight workers | 80,436/s |
| GPU ML-DSA verify, batch 1 | 8,448/s |
| GPU ML-DSA verify, batch 100 | 730,960/s |
| GPU ML-DSA verify, batch 1,000 | 1,738,893/s |
| GPU ML-DSA verify, batch 5,000 | 2,001,481/s |
| GPU ML-DSA verify, batch 10,000 | 2,073,029/s |

The CPU result saturates at four workers, matching the four vCPUs. This already
provides roughly 2.8 times the verification rate of the one-worker case and is
the lowest-complexity way to raise verification throughput.

The GPU figures above are steady-state kernel measurements. At batch 10,000,
the kernel took 4.824 ms. The measured setup containing host-to-device copies,
one warm-up launch, five measured launches, and device-to-host result copying
took 33.408 ms. From that measurement, a representative single copy-and-kernel
batch is approximately 9.3 ms, or about 1.08 million verifications per second.
This is still far above the present block pipeline, but it should not be
confused with end-to-end TPS.

## Interoperability check

The GPU test used chain-native values rather than synthetic cuPQC-only data:

- public key: 1,952 bytes
- signature: 3,309 bytes
- signed message/body hash: 32 bytes
- producer: the chain's CIRCL ML-DSA-65 implementation
- verifier: NVIDIA cuPQC ML-DSA-65

Verification passed at every tested batch size. This establishes practical
CIRCL-to-cuPQC interoperability for the tested vector.

## Profile findings

A CPU profile of a 10,000-transaction block on the local M2 attributed the
largest costs approximately as follows:

- 40%: `Transaction.FullHash`, including JSON marshaling and base64 encoding of
  the 3,309-byte signature and 1,952-byte public key
- 25%: public-key reconstruction/unpacking; a first-use transaction can parse
  the key for the address check and then parse it again during verification
- a few percent: state commit and state-root calculation

This explains why raw signature throughput is already much higher than block
throughput and why a GPU-only change cannot remove the dominant costs.

## Recommended implementation order

1. Add an immutable verified-transaction/mempool representation that computes
   and retains the body hash, full hash, and decoded public key once. Reuse the
   full hash for receipts, Merkle leaves, and indexes.
2. Add a bounded decoded-public-key cache keyed by address. Because an address
   is derived from the public key, an accepted address/key association is
   immutable.
3. Split block handling into deterministic stages: parallel stateless checks
   first, followed by serial nonce, balance, VM, and state execution.
4. Use a bounded CPU worker pool by default. The AWS result indicates that four
   verification workers can reach approximately 81,000 verifications per
   second without special hardware.
5. Add an optional cuPQC verifier only after profiling the optimized pipeline.
   Batch at least 1,000 signatures, fail closed, retain CIRCL as the correctness
   reference/fallback, and ensure validators without GPUs remain consensus
   compatible.
6. Consider a canonical binary transaction hash/encoding while the ML-DSA fork
   can still start from fresh genesis. At minimum, cache the exact legacy JSON
   hash if wire compatibility must be retained.
7. For a later phase, investigate speculative/parallel state execution with a
   deterministic conflict-resolution strategy.

A reasonable target to validate before introducing GPU operational complexity
is approximately 5,000-8,000 whole-chain TPS from caching and parallel CPU
prevalidation. This is an engineering estimate, not a measured result.

## Network and storage constraint

ML-DSA-65 signatures are large. Ten thousand signatures alone occupy 33.09 MB
before public keys and other transaction fields. JSON base64 encoding expands
binary fields by roughly one third. Once execution improves, propagation,
serialization, and storage will become limiting factors. Canonical binary wire
encoding, transaction batching/account abstraction, or an L2/rollup approach
will be needed for substantially higher sustained TPS. ML-DSA does not provide
native signature aggregation.

## Production cautions

- Treat GPU verification as an optional accelerator, never a consensus rule.
- Cross-check GPU results against CIRCL during rollout and preserve a CPU
  fallback.
- Review the NVIDIA cuPQC license, release maturity, and security posture before
  production adoption.
- Re-run interoperability vectors after every CIRCL, CUDA, driver, or cuPQC
  upgrade.

Relevant upstream documentation:

- [NVIDIA cuPQC public-key feature support](https://docs.nvidia.com/cuda/cupqc/overview/feature_cupqc_pk.html)
- [NVIDIA cuPQC getting started guide](https://docs.nvidia.com/cuda/cupqc/guides/getting_started.html)
- [NVIDIA cuPQC samples](https://github.com/NVIDIA/CUDALibrarySamples/tree/master/cuPQC)
- [AWS G6 instances](https://aws.amazon.com/ec2/instance-types/g6/)

## Cost and cleanup

The cumulative instance runtime across the short attempts was approximately
33.5 minutes. At the observed `g5.xlarge` on-demand price of $1.006/hour, the
compute upper bound is about $0.57; temporary EBS cost is negligible.

All experiment instances were terminated. The temporary S3 object and bucket,
security group, and experiment volumes were also removed. No production or
devnet resources were changed.
