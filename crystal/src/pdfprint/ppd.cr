require "./error"

# Parses PostScript Printer Description (PPD) files well enough to drive a
# Ghostscript-based print pipeline, the same way CUPS/foomatic-rip do. This is a
# faithful port of the Go `internal/ppd` package.
#
# It is deliberately a pragmatic subset of the full PPD spec (Adobe 4.3). We
# capture identity fields, device hints, UI options with their choices/defaults,
# and the Foomatic fields that encode the Ghostscript command line. Anything we
# do not understand is ignored rather than treated as an error.
module Pdfprint::Ppd
  # One selectable value of an Option (e.g. PageSize=A4).
  record Choice, keyword : String, translation : String, code : String

  # A UI-selectable option group (from *OpenUI ... *CloseUI).
  class Option
    property keyword : String
    property translation = ""
    property type = ""
    property default = ""
    property choices = [] of Choice
    property order : Int32

    def initialize(@keyword : String, @order : Int32)
    end

    # Looks up a choice by keyword (case-insensitive). Returns nil if absent.
    def choice(keyword : String) : Choice?
      @choices.find { |c| c.keyword.compare(keyword, case_insensitive: true) == 0 }
    end

    # The default choice, or nil if none is defined.
    def default_choice : Choice?
      return nil if @default.empty?
      choice(@default)
    end
  end

  # The parsed model of a printer description.
  class Ppd
    property nick_name = ""
    property model_name = ""
    property manufacturer = ""
    property default_resolution = "" # e.g. "600dpi"
    property language_level = ""
    property color_device = false
    property cups_filters = [] of String # raw *cupsFilter / *cupsFilter2 lines

    # Foomatic encoding of the Ghostscript command line.
    property foomatic_rip_command_line = ""
    # optionKeyword -> choiceKeyword -> code snippet
    property foomatic_settings = {} of String => Hash(String, String)

    # PageSize keyword -> physical "width height" in PostScript points, from
    # *PaperDimension lines — the authoritative media size we hand to gs.
    property paper_dimensions = {} of String => String

    property options = {} of String => Option # keyed by option keyword
    @option_order = [] of String

    # Returns the "w h" points string for a keyword (case-insensitive), or nil.
    def paper_dimension(keyword : String) : String?
      if v = @paper_dimensions[keyword]?
        return v
      end
      @paper_dimensions.each do |k, v|
        return v if k.compare(keyword, case_insensitive: true) == 0
      end
      nil
    end

    # Returns the named option (case-insensitive) or nil.
    def option(keyword : String) : Option?
      if o = @options[keyword]?
        return o
      end
      @options.each do |k, o|
        return o if k.compare(keyword, case_insensitive: true) == 0
      end
      nil
    end

    # Options in the order they first appeared in the file.
    def ordered_options : Array(Option)
      @option_order.compact_map { |k| @options[k]? }
    end

    protected def ensure_option(keyword : String) : Option
      if o = @options[keyword]?
        return o
      end
      o = Option.new(keyword, @option_order.size)
      @options[keyword] = o
      @option_order << keyword
      o
    end

    # Reads and parses a PPD from disk.
    def self.parse_file(path : String) : Ppd
      File.open(path) { |f| parse(f) }
    end

    # Reads and parses a PPD from an IO.
    def self.parse(io : IO) : Ppd
      p = Ppd.new
      open_option = "" # keyword of the option we are currently inside (OpenUI)

      scan_statements(io).each do |s|
        case
        when s.main == "NickName"
          p.nick_name = unquote(s.value)
        when s.main == "ModelName"
          p.model_name = unquote(s.value)
        when s.main == "Manufacturer"
          p.manufacturer = unquote(s.value)
        when s.main == "DefaultResolution"
          p.default_resolution = s.value.strip
        when s.main == "LanguageLevel"
          p.language_level = unquote(s.value)
        when s.main == "ColorDevice"
          p.color_device = s.value.strip.compare("true", case_insensitive: true) == 0
        when s.main == "cupsFilter" || s.main == "cupsFilter2"
          p.cups_filters << unquote(s.value)
        when s.main == "PaperDimension"
          # *PaperDimension Legal: "612 1008"
          p.paper_dimensions[s.option] = unquote(s.value) unless s.option.empty?
        when s.main == "FoomaticRIPCommandLine"
          p.foomatic_rip_command_line = unquote(s.value)
        when s.main == "FoomaticRIPOptionSetting"
          # *FoomaticRIPOptionSetting Option=Choice: "code"
          opt, choice = split_eq(s.option)
          if !opt.empty? && !choice.empty?
            (p.foomatic_settings[opt] ||= {} of String => String)[choice] = unquote(s.value)
          end
        when s.main == "OpenUI"
          # *OpenUI *PageSize/Media Size: PickOne
          kw = s.option.lchop('*')
          o = p.ensure_option(kw)
          o.translation = s.translation
          o.type = s.value.strip
          open_option = kw
        when s.main == "CloseUI"
          open_option = ""
        when s.main.starts_with?("Default")
          # *DefaultPageSize: Letter -> sets default for option "PageSize"
          kw = s.main.lchop("Default")
          if o = p.options[kw]?
            o.default = s.value.strip
          else
            # Default may appear before OpenUI; stash it.
            p.ensure_option(kw).default = s.value.strip
          end
        else
          # A choice line for the currently-open option:
          #   *PageSize A4/A4: "<PS code>"
          if !open_option.empty? && s.main == open_option && !s.option.empty?
            p.ensure_option(open_option).choices <<
              Choice.new(s.option, s.translation, unquote(s.value))
          end
        end
      end

      p
    end

    # One logical PPD line: *main option/translation: value
    private class Statement
      property main = ""
      property option = ""
      property translation = ""
      property value = ""
    end

    # Tokenizes a PPD into logical statements, joining the multi-line quoted
    # values that PPD uses for embedded PostScript.
    private def self.scan_statements(io : IO) : Array(Statement)
      lines = io.gets_to_end.lines
      stmts = [] of Statement
      i = 0
      while i < lines.size
        line = lines[i]
        i += 1
        trimmed = line.strip
        next if trimmed.empty? || trimmed.starts_with?("*%") || trimmed == "*End"
        next unless trimmed.starts_with?("*")

        # If the value opens a quote that does not close on this line, keep
        # reading until the closing quote (or a lone "*End").
        if open_quote_unclosed?(line)
          b = String::Builder.new
          b << line
          while i < lines.size
            nxt = lines[i]
            i += 1
            b << '\n'
            break if nxt.strip == "*End" # terminates the value; drop it
            b << nxt
            break if nxt.includes?('"')
          end
          line = b.to_s
        end

        if st = parse_statement(line)
          stmts << st
        end
      end
      stmts
    end

    # Reports whether line contains a value-opening '"' with no matching close.
    private def self.open_quote_unclosed?(line : String) : Bool
      colon = line.index(':')
      return false unless colon
      rest = line[(colon + 1)..]
      first = rest.index('"')
      return false unless first
      rest[(first + 1)..].index('"').nil?
    end

    # Parses a single (possibly multi-line-joined) PPD statement.
    private def self.parse_statement(line : String) : Statement?
      line = line.strip
      return nil unless line.starts_with?("*")
      line = line[1..] # drop leading '*'

      head = ""
      value = ""
      if colon = line.index(':')
        head = line[0...colon].strip
        value = line[(colon + 1)..].strip
      else
        head = line.strip
      end

      st = Statement.new
      # head is "MainKeyword" or "MainKeyword OptionToken"
      if sp = head.index(/[ \t]/)
        st.main = head[0...sp]
        opt_tok = head[(sp + 1)..].strip
        # Split translation off the option token at '/'.
        if slash = opt_tok.index('/')
          st.option = opt_tok[0...slash]
          st.translation = opt_tok[(slash + 1)..]
        else
          st.option = opt_tok
        end
      else
        st.main = head
      end
      st.value = value

      st.main.empty? ? nil : st
    end

    # Strips a single layer of surrounding double quotes and trims space.
    private def self.unquote(s : String) : String
      s = s.strip
      if s.size >= 2 && s[0] == '"'
        if last = s.rindex('"')
          return s[1...last] if last > 0
        end
      end
      s
    end

    # Splits "Option=Choice" into its parts.
    private def self.split_eq(s : String) : {String, String}
      if i = s.index('=')
        {s[0...i].strip, s[(i + 1)..].strip}
      else
        {s.strip, ""}
      end
    end
  end
end
