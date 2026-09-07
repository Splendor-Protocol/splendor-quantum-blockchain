// sqc-solc compiles a QSol-subset source file to SQC-VM v4 bytecode and prints
// the deployable hex plus the contract ABI (function selectors, event topics).
//
//	sqc-solc contract.qsol          # hex + ABI to stdout
//	sqc-solc -hex-only contract.qsol
//
// The emitted hex is what a TxDeploy carries in its data field. This tool is
// off-chain: nothing here runs in a validator.
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/splendor-protocol/splendor-quantum-blockchain/compiler"
)

func main() {
	hexOnly := flag.Bool("hex-only", false, "print only the bytecode hex")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: sqc-solc [-hex-only] <source.qsol>\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	src, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sqc-solc: %v\n", err)
		os.Exit(1)
	}
	c, err := compiler.Compile(string(src))
	if err != nil {
		fmt.Fprintf(os.Stderr, "sqc-solc: %v\n", err)
		os.Exit(1)
	}

	code := hex.EncodeToString(c.Bytecode)
	if *hexOnly {
		fmt.Println(code)
		return
	}

	type fnOut struct {
		Name      string   `json:"name"`
		Signature string   `json:"signature"`
		Selector  string   `json:"selector"`
		Params    []string `json:"params"`
		Returns   string   `json:"returns,omitempty"`
	}
	type evOut struct {
		Name      string `json:"name"`
		Signature string `json:"signature"`
		Topic     string `json:"topic0"`
	}
	out := struct {
		Contract  string  `json:"contract"`
		Bytecode  string  `json:"bytecode"`
		SizeBytes int     `json:"size_bytes"`
		Functions []fnOut `json:"functions"`
		Events    []evOut `json:"events"`
	}{Contract: c.Name, Bytecode: "0x" + code, SizeBytes: len(c.Bytecode)}

	for _, f := range c.Functions {
		out.Functions = append(out.Functions, fnOut{
			Name: f.Name, Signature: f.Signature,
			Selector: "0x" + hex.EncodeToString(f.Selector[:]),
			Params:   f.Params, Returns: f.Returns,
		})
	}
	for _, e := range c.Events {
		out.Events = append(out.Events, evOut{
			Name: e.Name, Signature: e.Signature,
			Topic: "0x" + hex.EncodeToString(e.Topic[:]),
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "sqc-solc: %v\n", err)
		os.Exit(1)
	}
}
