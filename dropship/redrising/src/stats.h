#pragma once
#include <windows.h>
#include <string>
#include <vector>

struct SystemStats {
    double cpu_percent;
    double memory_percent;
    uint64_t memory_total;
    uint64_t memory_used;
    uint64_t disk_total;
    uint64_t disk_used;
    double disk_percent;
    uint64_t uptime_seconds;
    double load_avg1;   // not applicable on Windows; set 0
    double load_avg5;   // not applicable; set 0
    double load_avg15;  // not applicable; set 0
};

struct OSInfo {
    std::string name;
    std::string version;
    std::string kernel;
    std::string platform;
    std::string hostname;
    std::string machine_id;
    std::string serial_number;
};

struct Application {
    std::string name;
    std::string version;
    std::string install_date;
    std::string publisher;
};

struct ProcessInfo {
    int32_t pid;
    std::string name;
    std::string status;
    double cpu; // percent
    double memory; // percent
    std::string command;
};

// Helper to collect all data and produce a JSON string for the heartbeat/sync request.
std::string buildStatsJson(const Config& cfg);
