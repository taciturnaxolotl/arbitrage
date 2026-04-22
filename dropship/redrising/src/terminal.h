#pragma once
#include "client.h"
#include <string>
#include <thread>
#include <atomic>

void startTerminalWS(const Config& cfg);
void stopTerminalWS();
bool isTerminalWSActive();