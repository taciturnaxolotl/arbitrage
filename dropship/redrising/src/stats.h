#pragma once
#include <windows.h>
#include <string>
#include <vector>

struct Config;

struct SystemStats {
    double cpu_percent;
    double memory_percent;
    uint64_t memory_total;
    uint64_t memory_used;
    uint64_t disk_total;
    uint64_t disk_used;
    double disk_percent;
    uint64_t uptime_seconds;
    double load_avg1;
    double load_avg5;
    double load_avg15;
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
    double cpu;
    double memory;
    std::string command;
};

SystemStats collectSystemStats();
OSInfo collectOSInfo(const Config& cfg);
std::vector<Application> collectApplications();
std::vector<ProcessInfo> collectProcesses();
std::string buildStatsJson(const Config& cfg);
