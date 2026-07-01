require "./spec_helper"

# Top-level helpers so spec example blocks can call them.
def sample_ppd
  Pdfprint::Ppd::Ppd.parse_file(SAMPLE_PPD)
end

def gs_args(cmd : Pdfprint::Gs::Command)
  cmd.args.join(' ')
end

# Port of internal/gs/gs_test.go.
module Pdfprint
  describe Gs do
    # The core requirement: 8.5x14 Legal, no scaling.
    it "builds Legal no-scaling from a PPD" do
      cmd = Gs.build(sample_ppd, Gs::Options.new(device: "pxlmono", page_size: "Legal", input_path: "job.pdf"))
      a = gs_args(cmd)
      a.should contain("-dDEVICEWIDTHPOINTS=612")
      a.should contain("-dDEVICEHEIGHTPOINTS=1008")
      a.should contain("-dFIXEDMEDIA")
      a.should contain("-dPDFFitPage=false") # strict 1:1
      a.should_not contain("-dPDFFitPage ") # must not enable fit
    end

    # Same must work with no PPD, from the built-in size table.
    it "builds Legal no-scaling from the built-in table (no PPD)" do
      cmd = Gs.build(nil, Gs::Options.new(device: "pxlmono", page_size: "Legal", input_path: "job.pdf"))
      a = gs_args(cmd)
      a.should contain("-dDEVICEWIDTHPOINTS=612")
      a.should contain("-dDEVICEHEIGHTPOINTS=1008")
      a.should contain("-dFIXEDMEDIA")
    end

    it "enables scaling with fit" do
      cmd = Gs.build(nil, Gs::Options.new(device: "pxlmono", page_size: "Legal", fit: true, input_path: "job.pdf"))
      a = gs_args(cmd)
      a.should contain("-dPDFFitPage")
      a.should_not contain("-dPDFFitPage=false")
    end

    it "infers the device from the PPD" do
      cmd = Gs.build(sample_ppd, Gs::Options.new(input_path: "job.pdf"))
      cmd.device.should eq("pxlcolor") # sample PPD's Foomatic line names pxlcolor
    end

    it "encodes copies and duplex" do
      cmd = Gs.build(nil, Gs::Options.new(device: "pxlcolor", copies: 3, duplex: Gs::Duplex::Long, input_path: "job.pdf"))
      a = gs_args(cmd)
      a.should contain("/NumCopies 3")
      a.should contain("/Duplex true /Tumble false")
    end

    it "errors when no device can be determined" do
      expect_raises(Error) do
        Gs.build(nil, Gs::Options.new(input_path: "job.pdf"))
      end
    end

    describe ".resolve_page_dims" do
      it "resolves from *PaperDimension, the built-in table, and rejects unknowns" do
        p = sample_ppd
        Gs.resolve_page_dims(p, "Legal").should eq({612, 1008})
        Gs.resolve_page_dims(nil, "a4").should eq({595, 842})
        Gs.resolve_page_dims(nil, "bogus").should be_nil
      end
    end
  end
end
