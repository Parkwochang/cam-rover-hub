#include <stella_vslam/config.h>
#include <stella_vslam/system.h>
#include <opencv2/imgcodecs.hpp>
#include <opencv2/imgproc.hpp>
#include <opencv2/video/tracking.hpp>

#include <cmath>
#include <algorithm>
#include <cstdint>
#include <iostream>
#include <memory>
#include <stdexcept>
#include <string>
#include <vector>

namespace {
constexpr uint32_t kMaxFrame = 1 << 20;

uint32_t read32(const unsigned char* data) {
    return (uint32_t(data[0]) << 24) | (uint32_t(data[1]) << 16) |
           (uint32_t(data[2]) << 8) | uint32_t(data[3]);
}

uint64_t read64(const unsigned char* data) {
    uint64_t value = 0;
    for (int i = 0; i < 8; ++i) value = (value << 8) | data[i];
    return value;
}

struct Options {
    std::string config;
    std::string vocab;
    std::string map;
    bool load = false;
};

struct Risk {
    int tracks = 0;
    double expansion = 0;
    bool safe = false;
};

Risk assess_visual_risk(const cv::Mat& previous, const cv::Mat& current) {
    Risk risk;
    if (previous.empty()) return risk;
    const cv::Rect area(current.cols / 4, current.rows * 35 / 100,
                        current.cols / 2, current.rows / 2);
    std::vector<cv::Point2f> before, after;
    cv::goodFeaturesToTrack(previous(area), before, 150, 0.01, 7);
    for (auto& point : before) { point.x += area.x; point.y += area.y; }
    if (before.empty()) return risk;
    std::vector<unsigned char> valid;
    std::vector<float> errors;
    cv::calcOpticalFlowPyrLK(previous, current, before, after, valid, errors,
                             cv::Size(15, 15), 3);
    const cv::Point2f center(current.cols * 0.5f, current.rows * 0.5f);
    double total = 0;
    for (size_t i = 0; i < before.size(); ++i) {
        if (!valid[i]) continue;
        const auto radial = before[i] - center;
        const auto flow = after[i] - before[i];
        const double radius2 = radial.dot(radial);
        if (radius2 < 100) continue;
        total += std::max(0.0, static_cast<double>(flow.dot(radial)) / radius2);
        ++risk.tracks;
    }
    if (risk.tracks > 0) risk.expansion = total / risk.tracks;
    risk.safe = risk.tracks >= 30 && risk.expansion < 0.015;
    return risk;
}

Options parse(int argc, char** argv) {
    Options result;
    for (int i = 1; i < argc; ++i) {
        const std::string key(argv[i]);
        if (key == "--load") { result.load = true; continue; }
        if (i + 1 >= argc) throw std::runtime_error("missing option value");
        if (key == "--config") result.config = argv[++i];
        else if (key == "--vocab") result.vocab = argv[++i];
        else if (key == "--map") result.map = argv[++i];
        else throw std::runtime_error("unknown option");
    }
    if (result.config.empty() || result.vocab.empty() || result.map.empty())
        throw std::runtime_error("config, vocab and map are required");
    return result;
}
}

int main(int argc, char** argv) {
    try {
        const auto options = parse(argc, argv);
        const auto config = std::make_shared<stella_vslam::config>(options.config);
        const int expected_cols = config->yaml_node_["Camera"]["cols"].as<int>();
        const int expected_rows = config->yaml_node_["Camera"]["rows"].as<int>();
        stella_vslam::system slam(config, options.vocab);
        if (options.load && !slam.load_map_database(options.map))
            throw std::runtime_error("map database could not be loaded");
        slam.startup(!options.load);
        if (options.load) slam.disable_mapping_module();

        unsigned char header[12];
        cv::Mat previous_gray;
        int consecutive_safe = 0;
        while (std::cin.read(reinterpret_cast<char*>(header), sizeof(header))) {
            const auto length = read32(header);
            if (length == 0 || length > kMaxFrame) throw std::runtime_error("invalid JPEG length");
            std::vector<unsigned char> jpeg(length);
            if (!std::cin.read(reinterpret_cast<char*>(jpeg.data()), length))
                throw std::runtime_error("truncated JPEG frame");
            const auto frame = cv::imdecode(jpeg, cv::IMREAD_COLOR);
            if (frame.empty()) { std::cout << "{\"type\":\"lost\"}" << std::endl; continue; }
            if (frame.cols != expected_cols || frame.rows != expected_rows)
                throw std::runtime_error("frame resolution differs from camera calibration");
            cv::Mat gray;
            cv::cvtColor(frame, gray, cv::COLOR_BGR2GRAY);
            const auto risk = assess_visual_risk(previous_gray, gray);
            previous_gray = gray;
            consecutive_safe = risk.safe ? std::min(consecutive_safe + 1, 3) : 0;
            std::cout << "{\"type\":\"risk\",\"safe\":" << (consecutive_safe >= 3 ? "true" : "false")
                      << ",\"tracks\":" << risk.tracks << ",\"expansion\":" << risk.expansion << "}" << std::endl;
            const double timestamp = static_cast<double>(read64(header + 4)) / 1e9;
            const auto pose = slam.feed_monocular_frame(frame, timestamp);
            if (!pose) { std::cout << "{\"type\":\"lost\"}" << std::endl; continue; }
            // Camera forward is +Z. Plot world X/Z as a relative top-down path.
            const double x = (*pose)(0, 3), y = (*pose)(2, 3);
            const double heading = std::atan2((*pose)(0, 2), (*pose)(2, 2));
            if (!std::isfinite(x) || !std::isfinite(y) || !std::isfinite(heading)) {
                std::cout << "{\"type\":\"lost\"}" << std::endl;
                continue;
            }
            // 1 means tracker returned a pose; it is not a metric confidence score.
            std::cout << "{\"type\":\"pose\",\"x\":" << x << ",\"y\":" << y
                      << ",\"heading\":" << heading << ",\"confidence\":1}" << std::endl;
        }
        if (!std::cin.eof() || std::cin.gcount() != 0)
            throw std::runtime_error("frame input failed or header truncated");
        slam.shutdown();
        if (!options.load && !slam.save_map_database(options.map))
            throw std::runtime_error("map database could not be saved");
        return 0;
    } catch (const std::exception& error) {
        std::cerr << "rover-vision: " << error.what() << std::endl;
        return 1;
    }
}
