/**
****************************************************************
* @file    JsonLite.h
* @brief   测试程序用最小 JSON 读取工具（不依赖 Qt 或第三方库）
* @author  Croc-DawnUI
* @date    2026-11-01
* @note    只解析本引擎生成的 UTF-8 JSON。
****************************************************************
*/
#pragma once

#include <cctype>
#include <limits>
#include <string>

namespace JsonLite
{
inline void SkipSpace(const std::string& _json, size_t& _pos)
{
    while (_pos < _json.size() && std::isspace(static_cast<unsigned char>(_json[_pos])))
    {
        ++_pos;
    }
}

// 返回 key 对应 value 的起始位置。
inline size_t FindValueStart(const std::string& _json, const std::string& _key)
{
    const std::string needle = "\"" + _key + "\"";
    size_t search = 0;
    while (search < _json.size())
    {
        size_t pos = _json.find(needle, search);
        if (pos == std::string::npos)
        {
            return pos;
        }
        search = pos + needle.size();
        pos = search;
        SkipSpace(_json, pos);
        if (pos >= _json.size() || _json[pos] != ':')
        {
            // 例如 "event":"phase" 中的值也包含 "phase"，必须继续找真正的 key。
            continue;
        }
        ++pos;
        SkipSpace(_json, pos);
        return pos;
    }
    return std::string::npos;
}

inline bool ExtractRaw(const std::string& _json, const std::string& _key, std::string& _raw)
{
    const size_t pos = FindValueStart(_json, _key);
    if (pos == std::string::npos || pos >= _json.size())
    {
        return false;
    }
    const size_t begin = pos;
    size_t end = pos;
    if (_json[pos] == '"')
    {
        end = pos + 1;
        while (end < _json.size())
        {
            if (_json[end] == '\\')
            {
                end += 2;
                continue;
            }
            if (_json[end] == '"')
            {
                ++end;
                break;
            }
            ++end;
        }
    }
    else if (_json[pos] == '{' || _json[pos] == '[')
    {
        const char open = _json[pos];
        const char close = (open == '{') ? '}' : ']';
        int depth = 0;
        bool inString = false;
        for (; end < _json.size(); ++end)
        {
            const char ch = _json[end];
            if (inString)
            {
                if (ch == '\\')
                {
                    ++end;
                    continue;
                }
                if (ch == '"')
                {
                    inString = false;
                }
                continue;
            }
            if (ch == '"')
            {
                inString = true;
            }
            else if (ch == open)
            {
                ++depth;
            }
            else if (ch == close)
            {
                --depth;
                if (depth == 0)
                {
                    ++end;
                    break;
                }
            }
        }
    }
    else
    {
        while (end < _json.size() && _json[end] != ',' && _json[end] != '}' && _json[end] != ']')
        {
            ++end;
        }
    }
    _raw = _json.substr(begin, end - begin);
    return true;
}

inline std::string Unescape(const std::string& _text)
{
    std::string result;
    for (size_t i = 0; i < _text.size(); ++i)
    {
        if (_text[i] != '\\' || i + 1 >= _text.size())
        {
            result.push_back(_text[i]);
            continue;
        }
        const char next = _text[++i];
        switch (next)
        {
        case 'n': result.push_back('\n'); break;
        case 'r': result.push_back('\r'); break;
        case 't': result.push_back('\t'); break;
        case '\\': result.push_back('\\'); break;
        case '/': result.push_back('/'); break;
        case '"': result.push_back('"'); break;
        default: result.push_back(next); break;
        }
    }
    return result;
}

inline std::string GetString(const std::string& _json, const std::string& _key, const std::string& _default = "")
{
    std::string raw;
    if (!ExtractRaw(_json, _key, raw) || raw.size() < 2 || raw.front() != '"')
    {
        return _default;
    }
    return Unescape(raw.substr(1, raw.size() - 2));
}

inline long long GetInt(const std::string& _json, const std::string& _key, long long _default = 0)
{
    std::string raw;
    if (!ExtractRaw(_json, _key, raw) || raw == "null")
    {
        return _default;
    }
    try
    {
        return std::stoll(raw);
    }
    catch (...)
    {
        return _default;
    }
}

inline double GetDouble(const std::string& _json, const std::string& _key,
                        double _default = std::numeric_limits<double>::quiet_NaN())
{
    std::string raw;
    if (!ExtractRaw(_json, _key, raw) || raw == "null" || raw.empty())
    {
        return _default;
    }
    try
    {
        return std::stod(raw);
    }
    catch (...)
    {
        return _default;
    }
}

inline std::string GetObjectValue(const std::string& _json, const std::string& _key)
{
    std::string raw;
    if (!ExtractRaw(_json, _key, raw) || raw.size() < 2 || raw.front() != '{')
    {
        return {};
    }
    return raw;
}
}
