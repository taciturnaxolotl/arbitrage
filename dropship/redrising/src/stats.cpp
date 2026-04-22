#include "stats.h"
#include "client.h"
#include <windows.h>
#include <tlhelp32.h>
#include <psapi.h>
#include <sstream>
#include <iomanip>

static ULONGLONG FileTimeToULL(const FILETIME& ft) {
    ULARGE_INTEGER li; li.LowPart = ft.dwLowDateTime; li.HighPart = ft.dwHighDateTime; return li.QuadPart;
}

static double GetCPUPercent() {
    static FILETIME prevIdle{}, prevKernel{}, prevUser{};
    FILETIME idle, kernel, user;
    if (!GetSystemTimes(&idle, &kernel, &user)) return 0.0;
    ULONGLONG idleDiff  = FileTimeToULL(idle) - FileTimeToULL(prevIdle);
    ULONGLONG kernelDiff= FileTimeToULL(kernel) - FileTimeToULL(prevKernel);
    ULONGLONG userDiff  = FileTimeToULL(user) - FileTimeToULL(prevUser);
    ULONGLONG total = kernelDiff + userDiff;
    double percent = 0.0;
    if (total > 0) percent = (double)(total - idleDiff) * 100.0 / (double)total;
    prevIdle = idle; prevKernel = kernel; prevUser = user;
    return percent;
}

static SystemStats collectSystemStats() {
    SystemStats ss{};
    ss.cpu_percent = GetCPUPercent();
    MEMORYSTATUSEX mem{}; mem.dwLength = sizeof(mem);
    if (GlobalMemoryStatusEx(&mem)) {
        ss.memory_total = mem.ullTotalPhys;
        ss.memory_used = mem.ullTotalPhys - mem.ullAvailPhys;
        ss.memory_percent = mem.dwMemoryLoad;
    }
    ULARGE_INTEGER freeBytes, totalBytes, totalFree;
    if (GetDiskFreeSpaceExW(L"C:/", &freeBytes, &totalBytes, &totalFree)) {
        ss.disk_total = totalBytes.QuadPart;
        ss.disk_used = totalBytes.QuadPart - freeBytes.QuadPart;
        ss.disk_percent = (double)ss.disk_used * 100.0 / (double)ss.disk_total;
    }
    ss.uptime_seconds = GetTickCount64() / 1000ULL;
    ss.load_avg1 = ss.load_avg5 = ss.load_avg15 = 0.0; // not applicable on Windows
    return ss;
}

static OSInfo collectOSInfo(const Config& cfg) {
    OSInfo oi{};
    oi.name = "Windows";
    OSVERSIONINFOEXW v{}; v.dwOSVersionInfoSize = sizeof(v);
    if (GetVersionExW((OSVERSIONINFOW*)&v)) {
        std::wstringstream ws;
        ws << L"" << v.dwMajorVersion << L"." << v.dwMinorVersion << L" (Build " << v.dwBuildNumber << L")";
        std::wstring wver = ws.str();
        oi.version.assign(wver.begin(), wver.end());
    }
    oi.kernel = oi.version; // same for simplicity
    oi.platform = "Windows";
    oi.hostname = cfg.hostname;
    oi.machine_id = ""; // could be derived from registry, left blank
    oi.serial_number = "";
    return oi;
}

static std::vector<ProcessInfo> collectProcesses() {
    std::vector<ProcessInfo> list;
    HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
    if (snap == INVALID_HANDLE_VALUE) return list;
    PROCESSENTRY32W pe{}; pe.dwSize = sizeof(pe);
    if (Process32FirstW(snap, &pe)) {
        do {
            ProcessInfo pi{};
            pi.pid = (int32_t)pe.th32ProcessID;
            pi.name.assign(pe.szExeFile, pe.szExeFile + wcslen(pe.szExeFile));
            pi.status = "running";
            pi.cpu = 0.0; pi.memory = 0.0; pi.command = "";
            list.push_back(std::move(pi));
        } while (Process32NextW(snap, &pe));
    }
    CloseHandle(snap);
    return list;
}

static std::vector<Application> collectApplications() {
    // Placeholder – empty list. Implement registry scanning if needed.
    return {};
}

std::string escapeJson(const std::string& s) {
    std::ostringstream o; for (auto c: s) {
        switch (c) {
            case '\\': o << "\\\\"; break;
            case '"': o << "\\\""; break;
            case '\b': o << "\\b"; break;
            case '\f': o << "\\f"; break;
            case '\n': o << "\\n"; break;
            case '\r': o << "\\r"; break;
            case '\t': o << "\\t"; break;
            default:
                if (static_cast<unsigned char>(c) < 0x20) {
                    o << "\\u" << std::hex << std::setw(4) << std::setfill('0') << (int)c;
                } else o << c;
        }
    } return o.str(); }

std::string buildStatsJson(const Config& cfg) {
    SystemStats ss = collectSystemStats();
    OSInfo oi = collectOSInfo(cfg);
    auto apps = collectApplications();
    auto procs = collectProcesses();
    std::ostringstream oss;
    oss << "{\"system_stats\":{"
        << "\"cpu_percent\":" << ss.cpu_percent << ","
        << "\"memory_percent\":" << ss.memory_percent << ","
        << "\"memory_total\":" << ss.memory_total << ","
        << "\"memory_used\":" << ss.memory_used << ","
        << "\"disk_total\":" << ss.disk_total << ","
        << "\"disk_used\":" << ss.disk_used << ","
        << "\"disk_percent\":" << ss.disk_percent << ","
        << "\"uptime_seconds\":" << ss.uptime_seconds << ","
        << "\"load_avg_1\":" << ss.load_avg1 << ","
        << "\"load_avg_5\":" << ss.load_avg5 << ","
        << "\"load_avg_15\":" << ss.load_avg15 << "},";
    oss << "\"os_info\":{"
        << "\"name\":\"" << escapeJson(oi.name) << "\",";
    oss << "\"version\":\"" << escapeJson(oi.version) << "\",";
    oss << "\"kernel\":\"" << escapeJson(oi.kernel) << "\",";
    oss << "\"platform\":\"" << escapeJson(oi.platform) << "\",";
    oss << "\"hostname\":\"" << escapeJson(oi.hostname) << "\",";
    oss << "\"machine_id\":\"" << escapeJson(oi.machine_id) << "\",";
    oss << "\"serial_number\":\"" << escapeJson(oi.serial_number) << "\"}";
    // Applications array
    oss << ",\"applications\":[";
    for (size_t i=0;i<apps.size();++i) {
        const auto &a = apps[i];
        oss << "{\"name\":\"" << escapeJson(a.name) << "\","
            << "\"version\":\"" << escapeJson(a.version) << "\","
            << "\"install_date\":\"" << escapeJson(a.install_date) << "\","
            << "\"publisher\":\"" << escapeJson(a.publisher) << "\"}";
        if (i+1<apps.size()) oss << ",";
    }
    oss << "]";
    // Processes array
    oss << ",\"processes\":[";
    for (size_t i=0;i<procs.size();++i) {
        const auto &p = procs[i];
        oss << "{\"pid\":" << p.pid << ","
            << "\"name\":\"" << escapeJson(p.name) << "\","
            << "\"status\":\"" << escapeJson(p.status) << "\","
            << "\"cpu_percent\":" << p.cpu << ","
            << "\"memory_percent\":" << p.memory << ","
            << "\"command\":\"" << escapeJson(p.command) << "\"}";
        if (i+1<procs.size()) oss << ",";
    }
    oss << "]}";
    return oss.str();
}
