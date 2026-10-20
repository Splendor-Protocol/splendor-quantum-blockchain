package main

import (
	"fmt"
	"github.com/splendor-protocol/splendor-quantum-blockchain/sdk/sqc-sdk/signer"
)

func main() {
	var seed [32]byte
	for i := 0; i < 32; i++ {
		seed[i] = 0x01
	}
	w, err := signer.DeriveFromSeed(seed)
	if err != nil {
		panic(err)
	}
	fmt.Println(w.Address.String())
}
