// Package fakellm — тестовый OpenAI-совместимый сервер (раздел 15 ТЗ).
//
// Реализуется на этапе Э3: SSE chat/completions, управляемые задержки,
// отказы до и после первого байта, /metrics с управляемыми running/waiting.
// Anthropic-режим не реализовывать.
package fakellm
