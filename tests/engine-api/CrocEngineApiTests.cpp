/**
****************************************************************
* @file    CrocEngineApiTests.cpp
* @brief   Croc Engine C ABI 独立 C++20 测试程序（不依赖 Qt）
* @author  Croc-DawnUI
* @date    2026-11-01
* @note    动态加载 DLL，验证真实收发、真实进度/速度/ETA、校验、提示、取消和错误。
****************************************************************
*/
#include "croc_engine_api.h"
#include "JsonLite.h"

#define NOMINMAX
#include <windows.h>

#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <filesystem>
#include <fstream>
#include <iostream>
#include <limits>
#include <stdexcept>
#include <string>
#include <thread>
#include <vector>

namespace fs = std::filesystem;

namespace
{
struct TestFailure : std::runtime_error
{
    using std::runtime_error::runtime_error;
};

void Require(bool _condition, const std::string& _message)
{
    if (!_condition)
    {
        throw TestFailure(_message);
    }
}

std::string ToUtf8(const fs::path& _path)
{
    const std::u8string u8 = _path.u8string();
    return std::string(u8.begin(), u8.end());
}

std::string EscapeJson(const std::string& _text)
{
    std::string result;
    for (const char ch : _text)
    {
        switch (ch)
        {
        case '\\': result += "\\\\"; break;
        case '"': result += "\\\""; break;
        case '\n': result += "\\n"; break;
        case '\r': result += "\\r"; break;
        case '\t': result += "\\t"; break;
        default: result.push_back(ch); break;
        }
    }
    return result;
}

uint64_t NowMs()
{
    return static_cast<uint64_t>(std::chrono::duration_cast<std::chrono::milliseconds>(
        std::chrono::steady_clock::now().time_since_epoch()).count());
}

void SleepMs(int _ms)
{
    std::this_thread::sleep_for(std::chrono::milliseconds(_ms));
}

// -----------------------------------------------------------------
// DLL 函数表
// -----------------------------------------------------------------
struct EngineApi
{
    HMODULE m_hModule = nullptr;
    decltype(&Croc_GetApiVersion) pfnGetApiVersion = nullptr;
    decltype(&Croc_GetEngineVersion) pfnGetEngineVersion = nullptr;
    decltype(&Croc_Initialize) pfnInitialize = nullptr;
    decltype(&Croc_Shutdown) pfnShutdown = nullptr;
    decltype(&Croc_StartSend) pfnStartSend = nullptr;
    decltype(&Croc_StartReceive) pfnStartReceive = nullptr;
    decltype(&Croc_CancelTask) pfnCancelTask = nullptr;
    decltype(&Croc_IsTaskFinished) pfnIsTaskFinished = nullptr;
    decltype(&Croc_GetTaskSnapshot) pfnGetTaskSnapshot = nullptr;
    decltype(&Croc_PollEvent) pfnPollEvent = nullptr;
    decltype(&Croc_RespondToPrompt) pfnRespondToPrompt = nullptr;
    decltype(&Croc_ReleaseTask) pfnReleaseTask = nullptr;
    decltype(&Croc_GetLastError) pfnGetLastError = nullptr;
    decltype(&Croc_GetTaskCount) pfnGetTaskCount = nullptr;
    decltype(&Croc_GetActiveTaskCount) pfnGetActiveTaskCount = nullptr;
    decltype(&Croc_GetDroppedEventCount) pfnGetDroppedEventCount = nullptr;

    bool Load(const fs::path& _path)
    {
        m_hModule = LoadLibraryW(_path.wstring().c_str());
        if (m_hModule == nullptr)
        {
            return false;
        }
#define LOAD_API(field, fn) \
        field = reinterpret_cast<decltype(&::fn)>(GetProcAddress(m_hModule, #fn)); \
        if (field == nullptr) { return false; }
        LOAD_API(pfnGetApiVersion, Croc_GetApiVersion);
        LOAD_API(pfnGetEngineVersion, Croc_GetEngineVersion);
        LOAD_API(pfnInitialize, Croc_Initialize);
        LOAD_API(pfnShutdown, Croc_Shutdown);
        LOAD_API(pfnStartSend, Croc_StartSend);
        LOAD_API(pfnStartReceive, Croc_StartReceive);
        LOAD_API(pfnCancelTask, Croc_CancelTask);
        LOAD_API(pfnIsTaskFinished, Croc_IsTaskFinished);
        LOAD_API(pfnGetTaskSnapshot, Croc_GetTaskSnapshot);
        LOAD_API(pfnPollEvent, Croc_PollEvent);
        LOAD_API(pfnRespondToPrompt, Croc_RespondToPrompt);
        LOAD_API(pfnReleaseTask, Croc_ReleaseTask);
        LOAD_API(pfnGetLastError, Croc_GetLastError);
        LOAD_API(pfnGetTaskCount, Croc_GetTaskCount);
        LOAD_API(pfnGetActiveTaskCount, Croc_GetActiveTaskCount);
        LOAD_API(pfnGetDroppedEventCount, Croc_GetDroppedEventCount);
#undef LOAD_API
        return true;
    }

    void Unload()
    {
        if (m_hModule != nullptr)
        {
            FreeLibrary(m_hModule);
            m_hModule = nullptr;
        }
    }
};

struct TestOptions
{
    fs::path stDllPath;
    fs::path stWorkDir;
    std::string strRelay = "127.0.0.1:9209";
    std::string strRelayPassword = "pass123";
    std::string strMode = "full";
    std::string strCode;
    std::string strSaveDir;
    std::string strFile;
    int nTimeoutSeconds = 60;
};

struct EventRecord
{
    std::string strJson;
    std::string strName;
    uint64_t nTaskId = 0;
    uint64_t nSequence = 0;
};

struct Snapshot
{
    std::string strJson;
    std::string strState;
    std::string strPhase;
    std::string strError;
    std::string strErrorCode;
    std::string strVerificationStatus;
    long long llFiles = 0;
    long long llBytesDone = 0;
    long long llBytesTotal = 0;
    long long llSessionBytes = 0;
    long long llFilesSkipped = 0;
    long long llFilesFailed = 0;
    double dSpeedBps = std::numeric_limits<double>::quiet_NaN();
    double dEtaSeconds = std::numeric_limits<double>::quiet_NaN();
};

// -----------------------------------------------------------------
// API 读取工具
// -----------------------------------------------------------------
std::string LastError(EngineApi& _api)
{
    std::vector<char> buffer(8192);
    int32_t required = 0;
    if (_api.pfnGetLastError(buffer.data(), static_cast<int32_t>(buffer.size()), &required) != CROC_OK)
    {
        return "Croc_GetLastError failed";
    }
    return std::string(buffer.data());
}

Snapshot GetSnapshot(EngineApi& _api, uint64_t _taskId)
{
    std::vector<char> buffer(64 * 1024);
    int32_t required = 0;
    int32_t result = _api.pfnGetTaskSnapshot(_taskId, buffer.data(), static_cast<int32_t>(buffer.size()), &required);
    if (result == CROC_ERR_BUFFER_TOO_SMALL)
    {
        buffer.assign(static_cast<size_t>(required) + 1024, '\0');
        result = _api.pfnGetTaskSnapshot(_taskId, buffer.data(), static_cast<int32_t>(buffer.size()), &required);
    }
    if (result != CROC_OK)
    {
        throw TestFailure("Croc_GetTaskSnapshot failed: " + std::to_string(result) + " " + LastError(_api));
    }
    Snapshot snapshot;
    snapshot.strJson = std::string(buffer.data());
    snapshot.strState = JsonLite::GetString(snapshot.strJson, "state");
    snapshot.strPhase = JsonLite::GetString(snapshot.strJson, "phase");
    snapshot.strError = JsonLite::GetString(snapshot.strJson, "error");
    snapshot.strErrorCode = JsonLite::GetString(snapshot.strJson, "error_code");
    const std::string totals = JsonLite::GetObjectValue(snapshot.strJson, "totals");
    snapshot.llFiles = JsonLite::GetInt(totals, "files");
    snapshot.llFilesSkipped = JsonLite::GetInt(totals, "files_skipped");
    snapshot.llFilesFailed = JsonLite::GetInt(totals, "files_failed");
    snapshot.llBytesDone = JsonLite::GetInt(totals, "bytes_done");
    snapshot.llBytesTotal = JsonLite::GetInt(totals, "bytes_total");
    snapshot.llSessionBytes = JsonLite::GetInt(totals, "session_bytes");
    const std::string transfer = JsonLite::GetObjectValue(snapshot.strJson, "transfer");
    snapshot.dSpeedBps = JsonLite::GetDouble(transfer, "speed_bps");
    snapshot.dEtaSeconds = JsonLite::GetDouble(transfer, "eta_seconds");
    const std::string verification = JsonLite::GetObjectValue(snapshot.strJson, "verification");
    snapshot.strVerificationStatus = JsonLite::GetString(verification, "status");
    return snapshot;
}

bool PollEvent(EngineApi& _api, EventRecord& _event)
{
    std::vector<char> buffer(256 * 1024);
    for (int attempt = 0; attempt < 3; ++attempt)
    {
        int32_t required = 0;
        const int32_t result = _api.pfnPollEvent(buffer.data(), static_cast<int32_t>(buffer.size()), &required);
        if (result == CROC_NO_EVENT)
        {
            return false;
        }
        if (result == CROC_EVENT_AVAILABLE)
        {
            _event.strJson = std::string(buffer.data());
            _event.strName = JsonLite::GetString(_event.strJson, "event");
            _event.nTaskId = static_cast<uint64_t>(JsonLite::GetInt(_event.strJson, "task_id"));
            _event.nSequence = static_cast<uint64_t>(JsonLite::GetInt(_event.strJson, "sequence"));
            return true;
        }
        if (result == CROC_ERR_BUFFER_TOO_SMALL)
        {
            buffer.assign(static_cast<size_t>(required) + 1024, '\0');
            continue;
        }
        throw TestFailure("Croc_PollEvent failed: " + std::to_string(result) + " " + LastError(_api));
    }
    throw TestFailure("Croc_PollEvent did not fit after growing the buffer");
}

bool TaskFinished(EngineApi& _api, uint64_t _taskId)
{
    int32_t finished = 0;
    Require(_api.pfnIsTaskFinished(_taskId, &finished) == CROC_OK, "Croc_IsTaskFinished failed");
    return finished != 0;
}

std::vector<EventRecord> WaitForTasks(EngineApi& _api, const std::vector<uint64_t>& _taskIds, int _timeoutSeconds)
{
    std::vector<EventRecord> events;
    const uint64_t deadline = NowMs() + static_cast<uint64_t>(_timeoutSeconds) * 1000;
    uint64_t lastEventMs = NowMs();
    while (NowMs() < deadline)
    {
        EventRecord event;
        while (PollEvent(_api, event))
        {
            events.push_back(event);
            lastEventMs = NowMs();
        }
        bool allFinished = true;
        for (const uint64_t taskId : _taskIds)
        {
            if (!TaskFinished(_api, taskId))
            {
                allFinished = false;
                break;
            }
        }
        if (allFinished && NowMs() - lastEventMs > 200)
        {
            return events;
        }
        SleepMs(20);
    }
    throw TestFailure("timed out waiting for tasks");
}

EventRecord WaitForEvent(EngineApi& _api, const std::string& _name, int _timeoutSeconds,
                         std::vector<EventRecord>* _allEvents = nullptr)
{
    const uint64_t deadline = NowMs() + static_cast<uint64_t>(_timeoutSeconds) * 1000;
    while (NowMs() < deadline)
    {
        EventRecord event;
        if (PollEvent(_api, event))
        {
            if (_allEvents != nullptr)
            {
                _allEvents->push_back(event);
            }
            if (event.strName == _name)
            {
                return event;
            }
            continue;
        }
        SleepMs(20);
    }
    throw TestFailure("timed out waiting for event " + _name);
}

bool ContainsPhase(const std::vector<EventRecord>& _events, uint64_t _taskId, const std::string& _phase)
{
    for (const EventRecord& event : _events)
    {
        if (event.strName == "phase" && event.nTaskId == _taskId &&
            JsonLite::GetString(event.strJson, "phase") == _phase)
        {
            return true;
        }
    }
    return false;
}

double MaxObservedSpeed(const std::vector<EventRecord>& _events, uint64_t _taskId)
{
    double maximum = 0.0;
    for (const EventRecord& event : _events)
    {
        if (event.strName != "progress" || event.nTaskId != _taskId)
        {
            continue;
        }
        const std::string transfer = JsonLite::GetObjectValue(event.strJson, "transfer");
        const double speed = JsonLite::GetDouble(transfer, "speed_bps");
        if (!std::isnan(speed))
        {
            maximum = std::max(maximum, speed);
        }
    }
    return maximum;
}

bool SawEta(const std::vector<EventRecord>& _events, uint64_t _taskId)
{
    for (const EventRecord& event : _events)
    {
        if (event.strName == "progress" && event.nTaskId == _taskId)
        {
            const std::string transfer = JsonLite::GetObjectValue(event.strJson, "transfer");
            if (!std::isnan(JsonLite::GetDouble(transfer, "eta_seconds")))
            {
                return true;
            }
        }
    }
    return false;
}

// -----------------------------------------------------------------
// 文件与配置工具
// -----------------------------------------------------------------
void WritePatternFile(const fs::path& _path, size_t _size)
{
    fs::create_directories(_path.parent_path());
    std::ofstream stream(_path, std::ios::binary | std::ios::trunc);
    Require(stream.good(), "cannot create test file: " + ToUtf8(_path));
    std::vector<unsigned char> buffer(64 * 1024);
    size_t written = 0;
    uint32_t state = 0x12345678u;
    while (written < _size)
    {
        const size_t chunk = std::min(buffer.size(), _size - written);
        for (size_t i = 0; i < chunk; ++i)
        {
            state = state * 1664525u + 1013904223u;
            buffer[i] = static_cast<unsigned char>(state >> 24);
        }
        stream.write(reinterpret_cast<const char*>(buffer.data()), static_cast<std::streamsize>(chunk));
        written += chunk;
    }
    Require(stream.good(), "cannot write test file: " + ToUtf8(_path));
}

void WriteFilledFile(const fs::path& _path, size_t _size, unsigned char _value)
{
    fs::create_directories(_path.parent_path());
    std::ofstream stream(_path, std::ios::binary | std::ios::trunc);
    Require(stream.good(), "cannot create test file: " + ToUtf8(_path));
    const std::vector<char> block(64 * 1024, static_cast<char>(_value));
    size_t written = 0;
    while (written < _size)
    {
        const size_t chunk = std::min(block.size(), _size - written);
        stream.write(block.data(), static_cast<std::streamsize>(chunk));
        written += chunk;
    }
}

bool FilesEqual(const fs::path& _left, const fs::path& _right)
{
    std::ifstream left(_left, std::ios::binary);
    std::ifstream right(_right, std::ios::binary);
    if (!left.good() || !right.good())
    {
        return false;
    }
    std::vector<char> leftBuffer(64 * 1024);
    std::vector<char> rightBuffer(64 * 1024);
    while (left.good() && right.good())
    {
        left.read(leftBuffer.data(), static_cast<std::streamsize>(leftBuffer.size()));
        right.read(rightBuffer.data(), static_cast<std::streamsize>(rightBuffer.size()));
        const std::streamsize leftCount = left.gcount();
        const std::streamsize rightCount = right.gcount();
        if (leftCount != rightCount || !std::equal(leftBuffer.begin(), leftBuffer.begin() + leftCount, rightBuffer.begin()))
        {
            return false;
        }
        if (leftCount == 0)
        {
            break;
        }
    }
    return true;
}

fs::path UniqueDir(const TestOptions& _opt, const std::string& _name)
{
    static int counter = 0;
    const fs::path path = _opt.stWorkDir / (_name + "-" + std::to_string(NowMs()) + "-" + std::to_string(++counter));
    fs::create_directories(path);
    return path;
}

std::string MakeSendConfig(const TestOptions& _opt, const std::string& _code, const std::vector<fs::path>& _files,
                           const std::string& _extra = "")
{
    std::string json = "{\"code\":\"" + EscapeJson(_code) + "\",\"files\":[";
    for (size_t i = 0; i < _files.size(); ++i)
    {
        if (i != 0)
        {
            json += ",";
        }
        json += "\"" + EscapeJson(ToUtf8(_files[i])) + "\"";
    }
    json += "],\"relay\":\"" + EscapeJson(_opt.strRelay) + "\",\"relay_password\":\"" +
            EscapeJson(_opt.strRelayPassword) + "\",\"disable_local\":true,\"no_compress\":true" + _extra + "}";
    return json;
}

std::string MakeReceiveConfig(const TestOptions& _opt, const std::string& _code, const fs::path& _path,
                              bool _autoAccept, bool _overwrite, const std::string& _extra = "")
{
    return "{\"code\":\"" + EscapeJson(_code) + "\",\"save_path\":\"" + EscapeJson(ToUtf8(_path)) +
           "\",\"relay\":\"" + EscapeJson(_opt.strRelay) + "\",\"relay_password\":\"" +
           EscapeJson(_opt.strRelayPassword) + "\",\"disable_local\":true,\"auto_accept\":" +
           (_autoAccept ? "true" : "false") + ",\"overwrite\":" + (_overwrite ? "true" : "false") + _extra + "}";
}

uint64_t StartSend(EngineApi& _api, const std::string& _json)
{
    uint64_t taskId = 0;
    std::vector<char> error(8192);
    const int32_t result = _api.pfnStartSend(const_cast<char*>(_json.c_str()), &taskId, error.data(),
                                             static_cast<int32_t>(error.size()));
    if (result != CROC_OK)
    {
        throw TestFailure("Croc_StartSend failed: " + std::to_string(result) + " " + std::string(error.data()));
    }
    return taskId;
}

uint64_t StartReceive(EngineApi& _api, const std::string& _json)
{
    uint64_t taskId = 0;
    std::vector<char> error(8192);
    const int32_t result = _api.pfnStartReceive(const_cast<char*>(_json.c_str()), &taskId, error.data(),
                                                static_cast<int32_t>(error.size()));
    if (result != CROC_OK)
    {
        throw TestFailure("Croc_StartReceive failed: " + std::to_string(result) + " " + std::string(error.data()));
    }
    return taskId;
}

int32_t StartSendResult(EngineApi& _api, const std::string& _json)
{
    uint64_t taskId = 0;
    std::vector<char> error(2048);
    return _api.pfnStartSend(const_cast<char*>(_json.c_str()), &taskId, error.data(),
                             static_cast<int32_t>(error.size()));
}

int32_t StartReceiveResult(EngineApi& _api, const std::string& _json)
{
    uint64_t taskId = 0;
    std::vector<char> error(2048);
    return _api.pfnStartReceive(const_cast<char*>(_json.c_str()), &taskId, error.data(),
                                static_cast<int32_t>(error.size()));
}

// -----------------------------------------------------------------
// 测试用例
// -----------------------------------------------------------------
void TestApiBasics(EngineApi& _api)
{
    Require(std::string(_api.pfnGetApiVersion()) == "1.0.0", "unexpected API version");
    Require(std::string(_api.pfnGetEngineVersion()) == "11.6.0", "unexpected engine version");
    Require(_api.pfnInitialize() == CROC_OK, "initialize failed");
    Require(_api.pfnInitialize() == CROC_ERR_ALREADY_INITIALIZED, "duplicate initialize must be rejected");
    Require(_api.pfnGetTaskCount() == 0, "new engine should have no tasks");
    Require(_api.pfnGetActiveTaskCount() == 0, "new engine should have no active tasks");
    Require(_api.pfnGetDroppedEventCount() == 0, "new engine should have no dropped events");
    Require(StartSendResult(_api, "{}") == CROC_ERR_INVALID_CONFIG, "invalid send config must be rejected");
    Require(StartReceiveResult(_api, "{}") == CROC_ERR_INVALID_CONFIG, "invalid receive config must be rejected");
    int32_t required = 0;
    std::vector<char> buffer(1024);
    Require(_api.pfnGetTaskSnapshot(99999, buffer.data(), static_cast<int32_t>(buffer.size()), &required) == CROC_ERR_TASK_NOT_FOUND,
            "unknown task must be rejected");
    Require(_api.pfnGetLastError(buffer.data(), static_cast<int32_t>(buffer.size()), &required) == CROC_OK,
            "GetLastError failed");
    std::cout << "  [ok] engine basics\n";
}

void TestSingleTransfer(EngineApi& _api, const TestOptions& _opt)
{
    const fs::path root = UniqueDir(_opt, "single");
    const fs::path sendDir = root / "send";
    const fs::path recvDir = root / "recv";
    fs::create_directories(sendDir);
    fs::create_directories(recvDir);
    const fs::path source = sendDir / "single.bin";
    WritePatternFile(source, 16 * 1024 * 1024);
    const std::string code = "s1n2-g3l4";

    const uint64_t receiver = StartReceive(_api, MakeReceiveConfig(_opt, code, recvDir, true, true));
    SleepMs(200);
    const uint64_t sender = StartSend(_api, MakeSendConfig(_opt, code, {source}, ",\"throttle_upload\":\"4m\""));
    const std::vector<EventRecord> events = WaitForTasks(_api, {receiver, sender}, _opt.nTimeoutSeconds);
    const Snapshot recvSnapshot = GetSnapshot(_api, receiver);
    const Snapshot sendSnapshot = GetSnapshot(_api, sender);
    Require(recvSnapshot.strState == "completed", "receiver not completed: " + recvSnapshot.strError);
    Require(sendSnapshot.strState == "completed", "sender not completed: " + sendSnapshot.strError);
    Require(recvSnapshot.strVerificationStatus == "passed", "receiver verification not passed");
    Require(sendSnapshot.strVerificationStatus == "not_applicable", "sender verification must be not_applicable");
    Require(recvSnapshot.llSessionBytes == 16 * 1024 * 1024, "receiver session bytes mismatch");
    Require(FilesEqual(source, recvDir / "single.bin"), "received file content mismatch");
    Require(ContainsPhase(events, receiver, "transferring"), "receiver transferring phase missing");
    Require(ContainsPhase(events, receiver, "verifying"), "receiver verifying phase missing");
    Require(ContainsPhase(events, sender, "awaiting_confirmation"), "sender awaiting_confirmation phase missing");
    Require(MaxObservedSpeed(events, receiver) > 0.0, "receiver speed never positive");
    Require(MaxObservedSpeed(events, sender) > 0.0, "sender speed never positive");
    Require(SawEta(events, receiver) || SawEta(events, sender), "ETA never observed");
    Require(_api.pfnReleaseTask(receiver) == CROC_OK, "release receiver failed");
    Require(_api.pfnReleaseTask(sender) == CROC_OK, "release sender failed");
    std::cout << "  [ok] single file transfer, progress, speed, ETA, verification\n";
}

void CopyPrefix(const fs::path& _source, const fs::path& _destination, size_t _bytes)
{
    std::ifstream input(_source, std::ios::binary);
    std::ofstream output(_destination, std::ios::binary | std::ios::trunc);
    Require(input.good() && output.good(), "cannot prepare resume destination");
    std::vector<char> buffer(64 * 1024);
    size_t remaining = _bytes;
    while (remaining > 0)
    {
        const size_t chunk = std::min(buffer.size(), remaining);
        input.read(buffer.data(), static_cast<std::streamsize>(chunk));
        Require(input.gcount() == static_cast<std::streamsize>(chunk), "cannot read resume prefix");
        output.write(buffer.data(), static_cast<std::streamsize>(chunk));
        remaining -= chunk;
    }
    output.close();
    fs::resize_file(_destination, fs::file_size(_source));
}

void TestResume(EngineApi& _api, const TestOptions& _opt)
{
    const fs::path root = UniqueDir(_opt, "resume");
    const fs::path sendDir = root / "send";
    const fs::path recvDir = root / "recv";
    fs::create_directories(sendDir);
    fs::create_directories(recvDir);
    const fs::path source = sendDir / "resume.bin";
    WritePatternFile(source, 4 * 1024 * 1024);
    const fs::path destination = recvDir / "resume.bin";
    CopyPrefix(source, destination, 2 * 1024 * 1024); // 后 2 MiB 为 0，Croc 会判定为缺失块
    const std::string code = "r1e2-s3u4";
    const uint64_t receiver = StartReceive(_api, MakeReceiveConfig(_opt, code, recvDir, true, false,
        ",\"prompt_timeout_seconds\":20"));
    SleepMs(200);
    const uint64_t sender = StartSend(_api, MakeSendConfig(_opt, code, {source}));
    const EventRecord prompt = WaitForEvent(_api, "prompt", 10);
    Require(JsonLite::GetString(prompt.strJson, "prompt_kind") == "overwrite", "resume prompt missing");
    Require(_api.pfnRespondToPrompt(receiver, const_cast<char*>(JsonLite::GetString(prompt.strJson, "prompt_id").c_str()),
                                    CROC_PROMPT_RESUME) == CROC_OK, "resume prompt failed");
    WaitForTasks(_api, {receiver, sender}, _opt.nTimeoutSeconds);
    const Snapshot recvSnapshot = GetSnapshot(_api, receiver);
    const Snapshot sendSnapshot = GetSnapshot(_api, sender);
    Require(recvSnapshot.strState == "completed", "resume receiver not completed: " + recvSnapshot.strError);
    Require(recvSnapshot.strVerificationStatus == "passed", "resume verification not passed");
    Require(FilesEqual(source, destination), "resume final content mismatch");
    Require(recvSnapshot.llSessionBytes > 0 && recvSnapshot.llSessionBytes < 4 * 1024 * 1024,
            "resume session bytes should exclude reused prefix");
    Require(sendSnapshot.llSessionBytes < 4 * 1024 * 1024, "sender resume session bytes mismatch");
    Require(_api.pfnReleaseTask(receiver) == CROC_OK && _api.pfnReleaseTask(sender) == CROC_OK,
            "release resume tasks failed");
    std::cout << "  [ok] partial-file resume; reused bytes are not counted as session bytes\n";
}

void TestMultiFileFolder(EngineApi& _api, const TestOptions& _opt)
{
    const fs::path root = UniqueDir(_opt, "folder");
    const fs::path sendDir = root / "payload";
    const fs::path recvDir = root / "recv";
    fs::create_directories(sendDir / "nested");
    fs::create_directories(sendDir / "empty");
    fs::create_directories(recvDir);
    WritePatternFile(sendDir / "alpha.txt", 12345);
    WritePatternFile(sendDir / "nested" / "beta.bin", 1024 * 1024 + 7);
    WritePatternFile(sendDir / "zero.dat", 0);
    const std::string code = "f5o6-l7d8";
    const uint64_t receiver = StartReceive(_api, MakeReceiveConfig(_opt, code, recvDir, true, true));
    SleepMs(200);
    const uint64_t sender = StartSend(_api, MakeSendConfig(_opt, code, {sendDir}));
    WaitForTasks(_api, {receiver, sender}, _opt.nTimeoutSeconds);
    const Snapshot snapshot = GetSnapshot(_api, receiver);
    Require(snapshot.strState == "completed", "folder receiver not completed: " + snapshot.strError);
    Require(snapshot.strVerificationStatus == "passed", "folder verification not passed");
    Require(snapshot.llFiles == 3, "folder file count mismatch");
    const fs::path recvRoot = recvDir / sendDir.filename();
    Require(FilesEqual(sendDir / "alpha.txt", recvRoot / "alpha.txt"), "alpha content mismatch");
    Require(FilesEqual(sendDir / "nested" / "beta.bin", recvRoot / "nested" / "beta.bin"), "beta content mismatch");
    Require(fs::exists(recvRoot / "zero.dat"), "empty file missing");
    Require(fs::exists(recvRoot / "empty") && fs::is_directory(recvRoot / "empty"), "empty folder missing");
    Require(_api.pfnReleaseTask(receiver) == CROC_OK, "release folder receiver failed");
    Require(_api.pfnReleaseTask(sender) == CROC_OK, "release folder sender failed");
    std::cout << "  [ok] multi-file/folder/empty file transfer and verification\n";
}

void TestPrompts(EngineApi& _api, const TestOptions& _opt)
{
    const fs::path root = UniqueDir(_opt, "prompt");
    const fs::path source = root / "prompt.bin";
    const fs::path recvDir = root / "recv";
    fs::create_directories(recvDir);
    WritePatternFile(source, 1024 * 1024);
    WriteFilledFile(recvDir / "prompt.bin", 1024 * 1024, 0x5A);

    const std::string code = "p9o8-m7n6";
    const uint64_t receiver = StartReceive(_api, MakeReceiveConfig(_opt, code, recvDir, false, false,
        ",\"prompt_timeout_seconds\":20"));
    SleepMs(200);
    const uint64_t sender = StartSend(_api, MakeSendConfig(_opt, code, {source}));
    EventRecord accept;
    try
    {
        accept = WaitForEvent(_api, "prompt", 10);
    }
    catch (...)
    {
        const Snapshot receiverState = GetSnapshot(_api, receiver);
        const Snapshot senderState = GetSnapshot(_api, sender);
        std::cerr << "prompt timeout receiver state=" << receiverState.strState << " phase=" << receiverState.strPhase
                  << " error=" << receiverState.strError << "\n";
        std::cerr << "prompt timeout sender state=" << senderState.strState << " phase=" << senderState.strPhase
                  << " error=" << senderState.strError << "\n";
        throw;
    }
    Require(JsonLite::GetString(accept.strJson, "prompt_kind") == "receive_accept", "expected receive_accept prompt");
    const std::string acceptId = JsonLite::GetString(accept.strJson, "prompt_id");
    Require(!acceptId.empty(), "accept prompt id missing");
    Require(_api.pfnRespondToPrompt(receiver, const_cast<char*>(acceptId.c_str()), CROC_PROMPT_ACCEPT) == CROC_OK,
            "accept prompt failed");
    const EventRecord overwrite = WaitForEvent(_api, "prompt", 10);
    Require(JsonLite::GetString(overwrite.strJson, "prompt_kind") == "overwrite", "expected overwrite prompt");
    const std::string overwriteId = JsonLite::GetString(overwrite.strJson, "prompt_id");
    Require(_api.pfnRespondToPrompt(receiver, const_cast<char*>(overwriteId.c_str()), CROC_PROMPT_OVERWRITE) == CROC_OK,
            "overwrite prompt failed");
    Require(_api.pfnRespondToPrompt(receiver, const_cast<char*>(overwriteId.c_str()), CROC_PROMPT_OVERWRITE) == CROC_ERR_PROMPT_NOT_FOUND,
            "duplicate prompt response must be rejected");
    WaitForTasks(_api, {receiver, sender}, _opt.nTimeoutSeconds);
    Require(GetSnapshot(_api, receiver).strState == "completed", "prompt receiver not completed");
    Require(FilesEqual(source, recvDir / "prompt.bin"), "overwritten file mismatch");
    Require(_api.pfnReleaseTask(receiver) == CROC_OK && _api.pfnReleaseTask(sender) == CROC_OK, "release prompt tasks failed");

    // 跳过覆盖时保留已有内容。
    WriteFilledFile(recvDir / "prompt.bin", 1024 * 1024, 0x5A);
    const fs::path existing = recvDir / "prompt.bin";
    const std::string code2 = "s9k8-p7s6";
    const uint64_t receiver2 = StartReceive(_api, MakeReceiveConfig(_opt, code2, recvDir, false, false,
        ",\"prompt_timeout_seconds\":20"));
    SleepMs(200);
    const uint64_t sender2 = StartSend(_api, MakeSendConfig(_opt, code2, {source}));
    const EventRecord accept2 = WaitForEvent(_api, "prompt", 10);
    Require(_api.pfnRespondToPrompt(receiver2, const_cast<char*>(JsonLite::GetString(accept2.strJson, "prompt_id").c_str()),
                                    CROC_PROMPT_ACCEPT) == CROC_OK, "accept prompt 2 failed");
    const EventRecord overwrite2 = WaitForEvent(_api, "prompt", 10);
    Require(JsonLite::GetString(overwrite2.strJson, "prompt_kind") == "overwrite", "expected overwrite prompt 2");
    Require(_api.pfnRespondToPrompt(receiver2, const_cast<char*>(JsonLite::GetString(overwrite2.strJson, "prompt_id").c_str()),
                                    CROC_PROMPT_SKIP) == CROC_OK, "skip prompt failed");
    WaitForTasks(_api, {receiver2, sender2}, _opt.nTimeoutSeconds);
    const Snapshot skipSnapshot = GetSnapshot(_api, receiver2);
    Require(skipSnapshot.strState == "completed", "skip receiver not completed");
    Require(skipSnapshot.llFilesSkipped == 1, "skip count mismatch");
    Require(skipSnapshot.strVerificationStatus == "skipped", "skip verification status mismatch");
    Require(FilesEqual(existing, recvDir / "prompt.bin"), "skip must preserve existing content");
    Require(_api.pfnReleaseTask(receiver2) == CROC_OK && _api.pfnReleaseTask(sender2) == CROC_OK, "release skip tasks failed");
    std::cout << "  [ok] receive acceptance, overwrite, skip, prompt isolation\n";
}

void TestCancelAndErrors(EngineApi& _api, const TestOptions& _opt)
{
    const fs::path root = UniqueDir(_opt, "errors");
    const fs::path recvDir = root / "recv";
    fs::create_directories(recvDir);
    const fs::path missing = root / "missing.bin";
    Require(StartSendResult(_api, "{\"code\":\"e1r2-r3o4\",\"files\":[\"" + EscapeJson(ToUtf8(missing)) + "\"]}") == CROC_ERR_INVALID_CONFIG,
            "missing source must be rejected");
    Require(StartReceiveResult(_api, "{\"code\":\"e1r2-r3o4\",\"save_path\":\"Z:\\\\not-exist-dir\"}") == CROC_ERR_INVALID_CONFIG,
            "missing save path must be rejected");
    Require(StartSendResult(_api, "{\"code\":\"e1r2-r3o4\",\"files\":[],\"unknown_field\":1}") == CROC_ERR_INVALID_CONFIG,
            "unknown field must be rejected");
    Require(_api.pfnCancelTask(99999) == CROC_ERR_TASK_NOT_FOUND, "cancel unknown task must fail");
    Require(_api.pfnReleaseTask(99999) == CROC_ERR_TASK_NOT_FOUND, "release unknown task must fail");
    Require(_api.pfnRespondToPrompt(99999, const_cast<char*>("x"), CROC_PROMPT_ACCEPT) == CROC_ERR_TASK_NOT_FOUND,
            "respond unknown task must fail");

    const uint64_t receiver = StartReceive(_api, MakeReceiveConfig(_opt, "c5a6-n7c8", recvDir, true, true));
    SleepMs(300);
    Require(_api.pfnCancelTask(receiver) == CROC_OK, "cancel failed");
    Require(_api.pfnCancelTask(receiver) == CROC_OK, "duplicate cancel must succeed");
    WaitForTasks(_api, {receiver}, 15);
    const Snapshot cancelled = GetSnapshot(_api, receiver);
    Require(cancelled.strState == "cancelled", "cancelled state mismatch");
    Require(cancelled.strPhase == "cancelled", "cancelled phase mismatch");
    Require(_api.pfnReleaseTask(receiver) == CROC_OK, "release cancelled task failed");
    Require(_api.pfnReleaseTask(receiver) == CROC_ERR_TASK_NOT_FOUND, "duplicate release must fail");

    const uint64_t running = StartReceive(_api, MakeReceiveConfig(_opt, "r1u2-n3n4", recvDir, true, true));
    SleepMs(100);
    Require(_api.pfnReleaseTask(running) == CROC_ERR_TASK_BUSY, "release running task must fail");
    Require(_api.pfnCancelTask(running) == CROC_OK, "cancel running task failed");
    WaitForTasks(_api, {running}, 15);
    Require(_api.pfnReleaseTask(running) == CROC_OK, "release running task failed");

    const uint64_t buffered = StartReceive(_api, MakeReceiveConfig(_opt, "b1u2-f3f4", recvDir, true, true));
    SleepMs(100);
    char tiny[8] = {};
    int32_t required = 0;
    Require(_api.pfnPollEvent(tiny, sizeof(tiny), &required) == CROC_ERR_BUFFER_TOO_SMALL,
            "small buffer must return CROC_ERR_BUFFER_TOO_SMALL");
    Require(required > static_cast<int32_t>(sizeof(tiny)), "required size must include NUL");
    EventRecord retry;
    Require(PollEvent(_api, retry), "event must remain queued after buffer-too-small");
    Require(retry.nSequence > 0, "event sequence missing");
    Require(_api.pfnCancelTask(buffered) == CROC_OK, "cancel buffered task failed");
    WaitForTasks(_api, {buffered}, 15);
    Require(_api.pfnReleaseTask(buffered) == CROC_OK, "release buffered task failed");

    TestOptions badRelay = _opt;
    badRelay.strRelay = "127.0.0.1:1";
    const uint64_t failed = StartReceive(_api, MakeReceiveConfig(badRelay, "n9e0-t1w2", recvDir, true, true));
    WaitForTasks(_api, {failed}, 20);
    const Snapshot failedSnapshot = GetSnapshot(_api, failed);
    Require(failedSnapshot.strState == "failed", "bad relay task must fail");
    Require(!failedSnapshot.strError.empty(), "bad relay error missing");
    Require(_api.pfnReleaseTask(failed) == CROC_OK, "release bad relay task failed");

    const fs::path source = root / "wrong-code.bin";
    WritePatternFile(source, 256 * 1024);
    const uint64_t wrongReceiver = StartReceive(_api, MakeReceiveConfig(_opt, "1234-gamma", recvDir, true, true));
    SleepMs(200);
    const uint64_t wrongSender = StartSend(_api, MakeSendConfig(_opt, "1234-alpha", {source}));
    WaitForTasks(_api, {wrongReceiver, wrongSender}, 20);
    Require(GetSnapshot(_api, wrongReceiver).strState == "failed", "wrong-code receiver must fail");
    Require(GetSnapshot(_api, wrongSender).strState == "failed", "wrong-code sender must fail");
    Require(_api.pfnReleaseTask(wrongReceiver) == CROC_OK && _api.pfnReleaseTask(wrongSender) == CROC_OK,
            "release wrong-code tasks failed");
    std::cout << "  [ok] cancel, duplicate cancel/release, invalid ids, bad prompt, bad relay\n";
}

void TestCancelActiveTransfer(EngineApi& _api, const TestOptions& _opt)
{
    const fs::path root = UniqueDir(_opt, "cancel-active");
    const fs::path sendDir = root / "send";
    const fs::path recvDir = root / "recv";
    fs::create_directories(sendDir);
    fs::create_directories(recvDir);
    const fs::path source = sendDir / "large.bin";
    WritePatternFile(source, 32 * 1024 * 1024);
    const std::string code = "c1a2-n3c4";
    const uint64_t receiver = StartReceive(_api, MakeReceiveConfig(_opt, code, recvDir, true, true));
    SleepMs(200);
    const uint64_t sender = StartSend(_api, MakeSendConfig(_opt, code, {source}, ",\"throttle_upload\":\"4m\""));
    const uint64_t deadline = NowMs() + 15000;
    bool sawBytes = false;
    while (NowMs() < deadline)
    {
        EventRecord event;
        while (PollEvent(_api, event))
        {
        }
        if (GetSnapshot(_api, sender).llBytesDone > 0 || GetSnapshot(_api, receiver).llBytesDone > 0)
        {
            sawBytes = true;
            break;
        }
        SleepMs(20);
    }
    Require(sawBytes, "active transfer never produced progress");
    Require(_api.pfnCancelTask(sender) == CROC_OK, "cancel active sender failed");
    WaitForTasks(_api, {sender}, 20);
    const Snapshot cancelled = GetSnapshot(_api, sender);
    Require(cancelled.strState == "cancelled", "cancelled active transfer state mismatch");
    Require(_api.pfnCancelTask(receiver) == CROC_OK, "cancel active receiver failed");
    WaitForTasks(_api, {receiver}, 20);
    Require(_api.pfnReleaseTask(sender) == CROC_OK, "release active sender failed");
    Require(_api.pfnReleaseTask(receiver) == CROC_OK, "release active receiver failed");
    std::cout << "  [ok] cancel during active transfer\n";
}

void TestMultiTask(EngineApi& _api, const TestOptions& _opt)
{
    const fs::path root = UniqueDir(_opt, "multi");
    const fs::path recvA = root / "recv-a";
    const fs::path recvB = root / "recv-b";
    fs::create_directories(recvA);
    fs::create_directories(recvB);
    const fs::path sourceA = root / "a.bin";
    const fs::path sourceB = root / "b.bin";
    WritePatternFile(sourceA, 512 * 1024 + 1);
    WritePatternFile(sourceB, 512 * 1024 + 3);
    const uint64_t receiverA = StartReceive(_api, MakeReceiveConfig(_opt, "m1u2-l3t4", recvA, true, true));
    const uint64_t receiverB = StartReceive(_api, MakeReceiveConfig(_opt, "n5u6-l7t8", recvB, true, true));
    SleepMs(200);
    const uint64_t senderA = StartSend(_api, MakeSendConfig(_opt, "m1u2-l3t4", {sourceA}));
    const uint64_t senderB = StartSend(_api, MakeSendConfig(_opt, "n5u6-l7t8", {sourceB}));
    WaitForTasks(_api, {receiverA, receiverB, senderA, senderB}, _opt.nTimeoutSeconds);
    Require(GetSnapshot(_api, receiverA).strState == "completed", "multi task A receive failed");
    Require(GetSnapshot(_api, receiverB).strState == "completed", "multi task B receive failed");
    Require(FilesEqual(sourceA, recvA / "a.bin"), "multi task A content mismatch");
    Require(FilesEqual(sourceB, recvB / "b.bin"), "multi task B content mismatch");
    Require(_api.pfnGetTaskCount() == 4, "task count should include finished unreleased tasks");
    for (const uint64_t taskId : {receiverA, receiverB, senderA, senderB})
    {
        Require(_api.pfnReleaseTask(taskId) == CROC_OK, "multi task release failed");
    }
    std::cout << "  [ok] two isolated concurrent transfers\n";
}

void TestShutdown(EngineApi& _api, const TestOptions& _opt)
{
    const fs::path recvDir = UniqueDir(_opt, "shutdown");
    StartReceive(_api, MakeReceiveConfig(_opt, "s1h2-u3t4", recvDir, true, true));
    SleepMs(200);
    Require(_api.pfnShutdown() == CROC_OK, "shutdown with active task failed");
    Require(_api.pfnGetTaskCount() == 0, "shutdown must release tasks");
    Require(_api.pfnGetActiveTaskCount() == 0, "shutdown must stop tasks");
    Require(_api.pfnInitialize() == CROC_OK, "reinitialize after shutdown failed");
    Require(_api.pfnShutdown() == CROC_OK, "second shutdown failed");
    Require(_api.pfnInitialize() == CROC_OK, "initialize after second shutdown failed");
    Require(_api.pfnShutdown() == CROC_OK, "final shutdown failed");
    std::cout << "  [ok] shutdown cancels active tasks and supports reinitialize\n";
}

int RunPeerMode(EngineApi& _api, const TestOptions& _opt)
{
    Require(_api.pfnInitialize() == CROC_OK, "initialize failed");
    uint64_t taskId = 0;
    if (_opt.strMode == "peer-send")
    {
        Require(!_opt.strCode.empty() && !_opt.strFile.empty(), "peer-send requires --code and --file");
        taskId = StartSend(_api, MakeSendConfig(_opt, _opt.strCode, {fs::path(_opt.strFile)}));
    }
    else
    {
        Require(!_opt.strCode.empty() && !_opt.strSaveDir.empty(), "peer-receive requires --code and --save");
        taskId = StartReceive(_api, MakeReceiveConfig(_opt, _opt.strCode, fs::path(_opt.strSaveDir), true, true));
    }
    WaitForTasks(_api, {taskId}, _opt.nTimeoutSeconds);
    const Snapshot snapshot = GetSnapshot(_api, taskId);
    std::cout << "peer task " << taskId << " state=" << snapshot.strState
              << " phase=" << snapshot.strPhase
              << " verification=" << snapshot.strVerificationStatus
              << " error=" << snapshot.strError << "\n";
    const bool ok = snapshot.strState == "completed" &&
                    (snapshot.strVerificationStatus == "passed" ||
                     snapshot.strVerificationStatus == "not_applicable");
    Require(_api.pfnReleaseTask(taskId) == CROC_OK, "release peer task failed");
    Require(_api.pfnShutdown() == CROC_OK, "shutdown peer mode failed");
    return ok ? 0 : 2;
}
}

int main(int argc, char** argv)
{
    TestOptions opt;
    opt.stDllPath = fs::path("dist") / "windows-x64" / "croc_engine.dll";
    opt.stWorkDir = fs::path("build") / "engine-api";
    try
    {
        for (int i = 1; i < argc; ++i)
        {
            const std::string arg = argv[i];
            auto next = [&]() -> std::string
            {
                if (i + 1 >= argc)
                {
                    throw TestFailure("missing value for " + arg);
                }
                return argv[++i];
            };
            if (arg == "--dll") { opt.stDllPath = fs::path(next()); }
            else if (arg == "--work") { opt.stWorkDir = fs::path(next()); }
            else if (arg == "--relay") { opt.strRelay = next(); }
            else if (arg == "--relay-password") { opt.strRelayPassword = next(); }
            else if (arg == "--mode") { opt.strMode = next(); }
            else if (arg == "--code") { opt.strCode = next(); }
            else if (arg == "--save") { opt.strSaveDir = next(); }
            else if (arg == "--file") { opt.strFile = next(); }
            else if (arg == "--timeout") { opt.nTimeoutSeconds = std::stoi(next()); }
            else { throw TestFailure("unknown argument: " + arg); }
        }
        if (opt.stDllPath.is_relative())
        {
            opt.stDllPath = fs::absolute(opt.stDllPath);
        }
        if (opt.stWorkDir.is_relative())
        {
            opt.stWorkDir = fs::absolute(opt.stWorkDir);
        }
    }
    catch (const std::exception& ex)
    {
        std::cerr << "argument error: " << ex.what() << "\n";
        return 1;
    }

    EngineApi api;
    if (!api.Load(opt.stDllPath))
    {
        std::cerr << "failed to load DLL or resolve exports: " << ToUtf8(opt.stDllPath) << "\n";
        return 1;
    }

    try
    {
        fs::create_directories(opt.stWorkDir);
        std::cout << "Croc Engine API test\n";
        if (opt.strMode != "full")
        {
            const int result = RunPeerMode(api, opt);
            api.Unload();
            return result;
        }
        TestApiBasics(api);
        TestSingleTransfer(api, opt);
        TestResume(api, opt);
        TestMultiFileFolder(api, opt);
        TestPrompts(api, opt);
        TestCancelAndErrors(api, opt);
        TestCancelActiveTransfer(api, opt);
        TestMultiTask(api, opt);
        TestShutdown(api, opt);
        std::cout << "ALL TESTS PASSED\n";
    }
    catch (const std::exception& ex)
    {
        std::cerr << "TEST FAILED: " << ex.what() << "\n";
        std::cerr << "last error: " << LastError(api) << "\n";
        try { api.pfnShutdown(); } catch (...) {}
        api.Unload();
        return 1;
    }
    api.Unload();
    return 0;
}
