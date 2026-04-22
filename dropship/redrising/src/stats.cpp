#include "stats.h"
#include "client.h"
#include "commands.h"
#include <windows.h>
#include <tlhelp32.h>
#include <psapi.h>
#include <sstream>
#include <iomanip>
#include <map>

static std::string w2s(const std::wstring& w) {
    int len = WideCharToMultiByte(CP_UTF8, 0, w.c_str(), (int)w.size(), nullptr, 0, nullptr, nullptr);
    std::string s(len, 0);
    WideCharToMultiByte(CP_UTF8, 0, w.c_str(), (int)w.size(), &s[0], len, nullptr, nullptr);
    return s;
}

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

SystemStats collectSystemStats() {
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
    ss.load_avg1 = ss.load_avg5 = ss.load_avg15 = 0.0;
    return ss;
}

OSInfo collectOSInfo(const Config& cfg) {
    OSInfo oi{};
    oi.name = "Windows";
    OSVERSIONINFOEXW v{}; v.dwOSVersionInfoSize = sizeof(v);
    if (GetVersionExW((OSVERSIONINFOW*)&v)) {
        std::wstringstream ws;
        ws << L"" << v.dwMajorVersion << L"." << v.dwMinorVersion << L" (Build " << v.dwBuildNumber << L")";
        std::wstring wver = ws.str();
        oi.version = w2s(wver);
    }
    oi.kernel = oi.version;
    oi.platform = "Windows";
    oi.hostname = cfg.hostname;

    // Machine ID from registry HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid
    HKEY hKey = NULL;
    if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, "SOFTWARE\\Microsoft\\Cryptography", 0, KEY_READ, &hKey) == ERROR_SUCCESS) {
        char buf[64] = {};
        DWORD bufSize = sizeof(buf);
        if (RegQueryValueExA(hKey, "MachineGuid", NULL, NULL, (LPBYTE)buf, &bufSize) == ERROR_SUCCESS) {
            oi.machine_id = buf;
        }
        RegCloseKey(hKey);
    }

    // Serial number from WMI via registry (ComputerSystemProduct)
    hKey = NULL;
    if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, "SYSTEM\\CurrentControlSet\\Control\\SystemInformation", 0, KEY_READ, &hKey) == ERROR_SUCCESS) {
        char buf[64] = {};
        DWORD bufSize = sizeof(buf);
        if (RegQueryValueExA(hKey, "SystemSerialNumber", NULL, NULL, (LPBYTE)buf, &bufSize) == ERROR_SUCCESS) {
            oi.serial_number = buf;
        }
        RegCloseKey(hKey);
    }
    return oi;
}

std::vector<ProcessInfo> collectProcesses() {
    static FILETIME prevSysIdle{}, prevSysKernel{}, prevSysUser{};
    FILETIME sysIdle, sysKernel, sysUser;
    ULONGLONG sysTotalDiff = 0;
    if (GetSystemTimes(&sysIdle, &sysKernel, &sysUser)) {
        ULONGLONG kernelDiff = FileTimeToULL(sysKernel) - FileTimeToULL(prevSysKernel);
        ULONGLONG userDiff = FileTimeToULL(sysUser) - FileTimeToULL(prevSysUser);
        sysTotalDiff = kernelDiff + userDiff;
        prevSysIdle = sysIdle; prevSysKernel = sysKernel; prevSysUser = sysUser;
    }

    MEMORYSTATUSEX memInfo{}; memInfo.dwLength = sizeof(memInfo);
    GlobalMemoryStatusEx(&memInfo);
    ULONGLONG totalPhysMem = memInfo.ullTotalPhys;

    std::vector<ProcessInfo> list;
    HANDLE snap = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
    if (snap == INVALID_HANDLE_VALUE) return list;
    PROCESSENTRY32W pe{}; pe.dwSize = sizeof(pe);
    if (Process32FirstW(snap, &pe)) {
        do {
            ProcessInfo pi{};
            pi.pid = (int32_t)pe.th32ProcessID;
            pi.ppid = (int32_t)pe.th32ParentProcessID;
            pi.name = w2s(pe.szExeFile);
            pi.status = "running";
            pi.num_threads = (int32_t)pe.cntThreads;

            HANDLE hProc = OpenProcess(PROCESS_QUERY_INFORMATION | PROCESS_VM_READ, FALSE, pe.th32ProcessID);
            if (hProc) {
                PROCESS_MEMORY_COUNTERS_EX pmc{};
                if (GetProcessMemoryInfo(hProc, (PROCESS_MEMORY_COUNTERS*)&pmc, sizeof(pmc))) {
                    pi.memory = (totalPhysMem > 0) ? (double)pmc.WorkingSetSize * 100.0 / (double)totalPhysMem : 0.0;
                    pi.rss = pmc.WorkingSetSize;
                    pi.vms = pmc.PagefileUsage;
                }

                FILETIME ftCreate, ftExit, ftKernel, ftUser;
                if (GetProcessTimes(hProc, &ftCreate, &ftExit, &ftKernel, &ftUser)) {
                    pi.create_time = (int64_t)(FileTimeToULL(ftCreate) / 10000);
                    if (sysTotalDiff > 0) {
                        static std::map<DWORD, ULONGLONG> prevProcTime;
                        ULONGLONG procTime = FileTimeToULL(ftKernel) + FileTimeToULL(ftUser);
                        auto it = prevProcTime.find(pe.th32ProcessID);
                        if (it != prevProcTime.end()) {
                            ULONGLONG procDiff = procTime - it->second;
                            pi.cpu = (double)procDiff * 100.0 / (double)sysTotalDiff;
                        }
                        prevProcTime[pe.th32ProcessID] = procTime;
                    }
                }

                wchar_t exePath[MAX_PATH] = {};
                DWORD exePathSize = MAX_PATH;
                if (QueryFullProcessImageNameW(hProc, 0, exePath, &exePathSize)) {
                    pi.exe = w2s(std::wstring(exePath, exePathSize));
                    pi.command = pi.exe;
                }

                // Get process owner username
                HANDLE hToken = NULL;
                if (OpenProcessToken(hProc, TOKEN_QUERY, &hToken)) {
                    DWORD needed = 0;
                    GetTokenInformation(hToken, TokenUser, NULL, 0, &needed);
                    if (GetLastError() == ERROR_INSUFFICIENT_BUFFER && needed > 0) {
                        std::vector<BYTE> buf(needed);
                        if (GetTokenInformation(hToken, TokenUser, buf.data(), needed, &needed)) {
                            TOKEN_USER* tu = (TOKEN_USER*)buf.data();
                            wchar_t name[256] = {}, domain[256] = {};
                            DWORD nameLen = 256, domainLen = 256;
                            SID_NAME_USE sidType;
                            if (LookupAccountSidW(NULL, tu->User.Sid, name, &nameLen, domain, &domainLen, &sidType)) {
                                pi.username = w2s(std::wstring(domain, domainLen));
                                pi.username += "\\";
                                pi.username += w2s(std::wstring(name, nameLen));
                            }
                        }
                    }
                    CloseHandle(hToken);
                }

                // Get handle count for num_fds
                DWORD handleCount = 0;
                if (GetProcessHandleCount(hProc, &handleCount)) {
                    pi.num_fds = (int32_t)handleCount;
                }

                // IO counters for read/write bytes
                IO_COUNTERS ioCounters{};
                if (GetProcessIoCounters(hProc, &ioCounters)) {
                    pi.read_bytes = ioCounters.ReadTransferCount;
                    pi.write_bytes = ioCounters.WriteTransferCount;
                }

                CloseHandle(hProc);
            }
            list.push_back(std::move(pi));
        } while (Process32NextW(snap, &pe));
    }
    CloseHandle(snap);
    return list;
}

static std::string readRegStr(HKEY root, const char* subkey, const char* value) {
    HKEY hKey = NULL;
    std::string result;
    if (RegOpenKeyExA(root, subkey, 0, KEY_READ, &hKey) == ERROR_SUCCESS) {
        char buf[512] = {};
        DWORD bufSize = sizeof(buf);
        if (RegQueryValueExA(hKey, value, NULL, NULL, (LPBYTE)buf, &bufSize) == ERROR_SUCCESS) {
            result = buf;
        }
        RegCloseKey(hKey);
    }
    return result;
}

std::vector<Application> collectApplications() {
    std::vector<Application> apps;
    HKEY hKey = NULL;
    const char* keys[] = {
        "SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\Uninstall",
        "SOFTWARE\\WOW6432Node\\Microsoft\\Windows\\CurrentVersion\\Uninstall"
    };
    for (int k = 0; k < 2; k++) {
        if (RegOpenKeyExA(HKEY_LOCAL_MACHINE, keys[k], 0, KEY_READ, &hKey) != ERROR_SUCCESS)
            continue;
        DWORD subkeyIndex = 0;
        char subkeyName[256] = {};
        DWORD subkeyNameSize = sizeof(subkeyName);
        while (RegEnumKeyExA(hKey, subkeyIndex, subkeyName, &subkeyNameSize, NULL, NULL, NULL, NULL) == ERROR_SUCCESS) {
            char fullSubkey[512];
            snprintf(fullSubkey, sizeof(fullSubkey), "%s\\%s", keys[k], subkeyName);
            std::string name = readRegStr(HKEY_LOCAL_MACHINE, fullSubkey, "DisplayName");
            if (name.empty()) {
                subkeyNameSize = sizeof(subkeyName);
                subkeyIndex++;
                continue;
            }
            Application app{};
            app.name = name;
            app.version = readRegStr(HKEY_LOCAL_MACHINE, fullSubkey, "DisplayVersion");
            app.install_date = readRegStr(HKEY_LOCAL_MACHINE, fullSubkey, "InstallDate");
            app.publisher = readRegStr(HKEY_LOCAL_MACHINE, fullSubkey, "Publisher");
            app.path = readRegStr(HKEY_LOCAL_MACHINE, fullSubkey, "InstallLocation");
            std::string uninstallStr = readRegStr(HKEY_LOCAL_MACHINE, fullSubkey, "UninstallString");
            if (uninstallStr.find("Program Files (x86)") != std::string::npos) {
                app.arch_kind = "x86";
            } else if (uninstallStr.find("Program Files") != std::string::npos) {
                app.arch_kind = "x64";
            }
            app.last_modified = readRegStr(HKEY_LOCAL_MACHINE, fullSubkey, "LastModified");
            apps.push_back(std::move(app));
            subkeyNameSize = sizeof(subkeyName);
            subkeyIndex++;
        }
        RegCloseKey(hKey);
    }
    return apps;
}

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
        << "\"name\":\"" << escapeJson(oi.name) << "\","
        << "\"version\":\"" << escapeJson(oi.version) << "\","
        << "\"kernel\":\"" << escapeJson(oi.kernel) << "\","
        << "\"platform\":\"" << escapeJson(oi.platform) << "\","
        << "\"hostname\":\"" << escapeJson(oi.hostname) << "\","
        << "\"machine_id\":\"" << escapeJson(oi.machine_id) << "\","
        << "\"serial_number\":\"" << escapeJson(oi.serial_number) << "\"}";
    oss << ",\"applications\":[";
    for (size_t i=0;i<apps.size();++i) {
        const auto &a = apps[i];
        oss << "{\"name\":\"" << escapeJson(a.name) << "\","
            << "\"version\":\"" << escapeJson(a.version) << "\","
            << "\"install_date\":\"" << escapeJson(a.install_date) << "\","
            << "\"publisher\":\"" << escapeJson(a.publisher) << "\","
            << "\"path\":\"" << escapeJson(a.path) << "\","
            << "\"arch_kind\":\"" << escapeJson(a.arch_kind) << "\","
            << "\"last_modified\":\"" << escapeJson(a.last_modified) << "\","
            << "\"signed_by\":[";
        for (size_t j=0;j<a.signed_by.size();++j) {
            oss << "\"" << escapeJson(a.signed_by[j]) << "\"";
            if (j+1<a.signed_by.size()) oss << ",";
        }
        oss << "]}";
        if (i+1<apps.size()) oss << ",";
    }
    oss << "]";
    oss << ",\"processes\":[";
    for (size_t i=0;i<procs.size();++i) {
        const auto &p = procs[i];
        oss << "{\"pid\":" << p.pid << ","
            << "\"name\":\"" << escapeJson(p.name) << "\","
            << "\"status\":\"" << escapeJson(p.status) << "\","
            << "\"cpu_percent\":" << p.cpu << ","
            << "\"memory_percent\":" << p.memory << ","
            << "\"command\":\"" << escapeJson(p.command) << "\","
            << "\"exe\":\"" << escapeJson(p.exe) << "\","
            << "\"cwd\":\"" << escapeJson(p.cwd) << "\","
            << "\"username\":\"" << escapeJson(p.username) << "\","
            << "\"ppid\":" << p.ppid << ","
            << "\"create_time\":" << p.create_time << ","
            << "\"num_threads\":" << p.num_threads << ","
            << "\"num_fds\":" << p.num_fds << ","
            << "\"rss\":" << p.rss << ","
            << "\"vms\":" << p.vms << ","
            << "\"read_bytes\":" << p.read_bytes << ","
            << "\"write_bytes\":" << p.write_bytes << "}";
        if (i+1<procs.size()) oss << ",";
    }
    oss << "]}";
    return oss.str();
}
