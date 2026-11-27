# hyped/defmt_decoder

Go bindings for [defmt-decoder](https://crates.io/crates/defmt-decoder) through a Rust C FFI.

```go
decoders, err := defmt_decoder.Init([]string{"firmware.elf"})
if err != nil {
	log.Fatal(err) // with ErrorInAtLeastOneEntry, the paths that loaded still have a decoder
}

dec := decoders[0]
go func() {
	io.Copy(dec, rttSource) // raw bytes from the target
	dec.Close()
}()

scanner := bufio.NewScanner(dec) // one line per frame, plus a "└─ module @ file:line" line if the ELF has debug info
for scanner.Scan() {
	fmt.Println(scanner.Text())
}
```

`Init` can only be called once per process. Each `Decoder` is an `io.ReadWriteCloser`: raw
bytes go in through `Write`, and decoded text comes out of `Read`, which blocks until a frame is
available or the decoder is closed. Malformed frames are logged and skipped.

# Memory Safety
The rust wrapper focuses on the design, maintainability, and memory safety rather than performance. As a result, every stream is behind a `std::sync::Mutex`, and nothing is leaked onto the heap to have no unsafe {} usage outside of the FFI layer
