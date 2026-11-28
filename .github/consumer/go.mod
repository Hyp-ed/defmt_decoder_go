// Uses defmt_decoder_go the way a user would, see .github/workflows/consumer_test.yml. Having
// its own go.mod also keeps it out of the defmt_decoder_go module zip. The require line is
// added by `go get` in CI.
module consumer

go 1.27.3
