require "./error"
require "./ppd"

# Builds a Ghostscript command line from a parsed PPD plus the caller's render
# options, mirroring what foomatic-rip does: pick the output device, resolution,
# page geometry and duplex, then let Ghostscript translate the PDF into the
# printer's native language. Faithful port of the Go `internal/gs` package.
module Pdfprint::Gs
  # Duplex mode.
  enum Duplex
    None
    Long  # long-edge binding (portrait book)
    Short # short-edge binding (calendar)
  end

  # Controls how the PDF is rasterized/translated.
  class Options
    property device : String        # -sDEVICE override; if empty, inferred from PPD
    property resolution : String    # e.g. "600"; if empty, from PPD DefaultResolution
    property page_size : String     # PPD PageSize keyword; default from PPD
    property duplex : Duplex
    property copies : Int32         # <=1 means single
    property fit : Bool             # false = print 1:1, NO scaling; true = scale to page
    property color : Bool?          # nil = follow PPD/device default; else force color/mono
    property input_path : String    # path to input PDF, or "-" for stdin
    property gs_binary : String     # path to gs / gswin64c.exe; default "gs"
    property extra : Array(String)

    def initialize(@device = "", @resolution = "", @page_size = "",
                   @duplex = Duplex::None, @copies = 0, @fit = false,
                   @color = nil, @input_path = "", @gs_binary = "",
                   @extra = [] of String)
    end
  end

  # A fully-resolved Ghostscript invocation.
  struct Command
    getter binary : String
    getter args : Array(String)
    getter device : String

    def initialize(@binary : String, @args : Array(String), @device : String)
    end

    # Renders the command for logging / --dry-run.
    def to_s(io : IO) : Nil
      io << @binary << ' ' << @args.join(' ')
    end
  end

  # Pulls -sDEVICE=<name> out of a Foomatic command line.
  DEVICE_RE = /-sDEVICE=([A-Za-z0-9_.-]+)/
  # Pulls the first two numbers out of a PageSize choice's PostScript code,
  # e.g. "<</PageSize[612 1008]...>>" -> 612, 1008.
  DIMS_RE = /\[\s*([0-9.]+)\s+([0-9.]+)/

  # Common media keywords -> PostScript point dimensions, so --page-size works
  # even without a PPD. Points = 1/72 inch. Legal (8.5x14") = 612 x 1008.
  KNOWN_SIZES = {
    "letter"    => {612, 792},
    "legal"     => {612, 1008},
    "a4"        => {595, 842},
    "a3"        => {842, 1191},
    "tabloid"   => {792, 1224},
    "ledger"    => {1224, 792},
    "executive" => {522, 756},
    "statement" => {396, 612},
  }

  # Picks a Ghostscript output device for a PPD, or "" if it cannot.
  # Order: Foomatic command line -> cupsFilter hints -> nickname heuristics.
  def self.infer_device(p : Ppd::Ppd) : String
    if m = DEVICE_RE.match(p.foomatic_rip_command_line)
      return m[1]
    end
    joined = p.cups_filters.join(' ').downcase
    nick = "#{p.nick_name} #{p.model_name}".downcase
    hay = "#{joined} #{nick}"
    case
    when hay.includes?("postscript") || joined.includes?("pdftops")
      "ps2write"
    when hay.includes?("pcl-xl") || hay.includes?("pclxl") || hay.includes?("pcl6") || hay.includes?("pclm")
      p.color_device ? "pxlcolor" : "pxlmono"
    when hay.includes?("pcl")
      "ljet4"
    else
      ""
    end
  end

  # Resolves Options + PPD into a concrete Ghostscript command.
  def self.build(p : Ppd::Ppd?, o : Options) : Command
    bin = o.gs_binary.empty? ? "gs" : o.gs_binary

    device = o.device
    device = infer_device(p) if device.empty? && p
    if device.empty?
      raise Error.new("could not determine Ghostscript device from PPD; pass --device (e.g. pxlcolor, pxlmono, ljet4, ps2write)")
    end

    res = o.resolution
    if res.empty? && p
      res = p.default_resolution.downcase.rchop("dpi").strip
    end

    args = [
      "-q",               # quiet: keep stdout clean for the raw stream
      "-dBATCH",          # exit after processing
      "-dNOPAUSE",        # no per-page pause
      "-dSAFER",          # restricted file access (default in modern gs)
      "-dNOINTERPOLATE",  # match device pixels; sharper text on lasers
      "-sstdout=%stderr", # route gs messages to stderr, never stdout
      "-sOutputFile=%stdout",
      "-sDEVICE=#{device}",
    ]
    args << "-r#{res}" unless res.empty?

    # Color / mono coercion where the device supports it.
    unless o.color.nil?
      if o.color
        # pxlmono has no color; caller should choose pxlcolor via --device.
        if device == "pxlmono"
          raise Error.new("device #{device.inspect} is monochrome; use --device pxlcolor for color")
        end
      else
        # Force grayscale rendering regardless of device.
        args << "-dProcessColorModel=/DeviceGray"
        args << "-sColorConversionStrategy=Gray"
      end
    end

    # Page geometry. Resolve the target media size in points and lock it with
    # -dFIXEDMEDIA. This is the only reliable way to force exact media; without
    # fixing the size gs would default to Letter.
    if dims = resolve_page_dims(p, o.page_size)
      w, h = dims
      args << "-dDEVICEWIDTHPOINTS=#{w}"
      args << "-dDEVICEHEIGHTPOINTS=#{h}"
      args << "-dFIXEDMEDIA"
    end

    # Scaling policy. Default (fit=false) is strict 1:1, no scaling.
    if o.fit
      args << "-dPDFFitPage"
    else
      args << "-dPDFFitPage=false"
    end

    # Duplex / copies go through setpagedevice (not media, so safe after init).
    if snippet = options_snippet(o)
      args << "-c" << snippet << "-f"
    end

    args.concat(o.extra)

    input = o.input_path.empty? ? "-" : o.input_path
    if input == "-"
      args << "-_" # read PDF/PS from stdin
    else
      args << input
    end

    Command.new(bin, args, device)
  end

  # Resolves the target media size in points. Prefers, in order: the PPD's
  # *PaperDimension for the keyword, the numbers embedded in the PPD PageSize
  # choice code, then the built-in size table. When key is empty it uses the
  # PPD's default PageSize. Returns nil if nothing matches (gs then falls back
  # to the PDF's own MediaBox — still no scaling).
  def self.resolve_page_dims(p : Ppd::Ppd?, key : String) : {Int32, Int32}?
    if p
      if key.empty?
        if opt = p.option("PageSize")
          key = opt.default
        end
      end
      unless key.empty?
        if wh = p.paper_dimension(key)
          if dims = parse_two_nums(wh)
            return dims
          end
        end
        if opt = p.option("PageSize")
          if ch = opt.choice(key)
            if m = DIMS_RE.match(ch.code)
              if dims = parse_two_nums("#{m[1]} #{m[2]}")
                return dims
              end
            end
          end
        end
      end
    end
    KNOWN_SIZES[key.downcase]?
  end

  # Builds the setpagedevice prologue for duplex and copies. Returns nil when
  # there is nothing to set.
  def self.options_snippet(o : Options) : String?
    kv = [] of String
    case o.duplex
    when .long?
      kv << "/Duplex true /Tumble false"
    when .short?
      kv << "/Duplex true /Tumble true"
    when .none?
      kv << "/Duplex false"
    end
    kv << "/NumCopies #{o.copies}" if o.copies > 1
    return nil if kv.empty?
    "<< #{kv.join(' ')} >> setpagedevice"
  end

  # Parses "W H" (possibly fractional) into rounded integer points.
  def self.parse_two_nums(s : String) : {Int32, Int32}?
    f = s.split
    return nil if f.size < 2
    w = f[0].to_f?
    h = f[1].to_f?
    return nil if w.nil? || h.nil?
    {(w + 0.5).to_i, (h + 0.5).to_i}
  end
end
