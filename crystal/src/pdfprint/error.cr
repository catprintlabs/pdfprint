module Pdfprint
  # Raised for user-facing failures (bad options, unresolvable device, etc.),
  # mirroring the errors the Go implementation returns.
  class Error < Exception
  end
end
