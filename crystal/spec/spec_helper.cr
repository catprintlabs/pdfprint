require "spec"
require "../src/pdfprint/ppd"
require "../src/pdfprint/gs"

# Absolute path to the shared test fixtures in the repo root's testdata/.
# __DIR__ is crystal/spec, so ../../testdata is the repo-level testdata dir.
SAMPLE_PPD = File.join(__DIR__, "..", "..", "testdata", "sample.ppd")
