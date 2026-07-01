require "./spec_helper"

# Port of internal/ppd/ppd_test.go.
module Pdfprint
  describe Ppd do
    it "parses identity fields" do
      p = Ppd::Ppd.parse_file(SAMPLE_PPD)
      p.manufacturer.should eq("HP")
      p.nick_name.should contain("PCL-XL")
      p.color_device.should be_true
      p.default_resolution.should eq("600dpi")
    end

    it "captures the Foomatic command line" do
      p = Ppd::Ppd.parse_file(SAMPLE_PPD)
      p.foomatic_rip_command_line.should contain("-sDEVICE=pxlcolor")
    end

    it "parses the PageSize option, choices and default" do
      p = Ppd::Ppd.parse_file(SAMPLE_PPD)
      opt = p.option("PageSize")
      opt.should_not be_nil
      opt = opt.not_nil!
      opt.default.should eq("Letter")
      opt.choices.size.should eq(3)

      a4 = opt.choice("A4")
      a4.should_not be_nil
      a4.not_nil!.code.should contain("595 842")

      dc = opt.default_choice
      dc.should_not be_nil
      dc.not_nil!.keyword.should eq("Letter")
    end

    it "parses the Duplex option" do
      p = Ppd::Ppd.parse_file(SAMPLE_PPD)
      opt = p.option("Duplex")
      opt.should_not be_nil
      opt.not_nil!.choice("DuplexNoTumble").should_not be_nil
    end
  end
end
