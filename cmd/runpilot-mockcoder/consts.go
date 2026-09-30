// Package main — константы runpilot-mockcoder (таблица R2): chrome, ввод, UTF-8.
package main

import "time"

const (
	// width — ширина рисуемых строк (в реальной панели 200 колонок лишние
	// колонки остаются пустыми после ESC[2J).
	width = 100
	// toolPause — пауза «инструмента» между потоковыми POST.
	toolPause = 500 * time.Millisecond
	// inputRow — строка поля ввода в нарисованном хроме.
	inputRow = 2
	// docRow — начало области документа (история терминала).
	docRow = 6

	// defaultPosts — потоковых POST на ход по умолчанию.
	defaultPosts = 3
	// fatalExitCode — код выхода при фатальной ошибке.
	fatalExitCode = 2

	// Байты терминала (read loop).
	ctrlCByte     = 3
	delByte       = 127
	backspaceByte = 8
	ctrlUByte     = 21
	escByte       = 27
	printableMin  = 32

	// UTF-8: high=1xxxxxxx, lead2=110xxxxx, lead3=1110xxxx, lead4=11110xxx.
	utf8High      = 0x80
	utf8Lead2     = 0xC0
	utf8Lead3     = 0xE0
	utf8Lead4     = 0xF0
	utf8Lead4Mask = 0xF8
	// utf8ContMask/Shift — 6 бит payload в continuation-байте.
	utf8ContMask  = 0x3F
	utf8ContShift = 6
	// utf8Mask2/3/4 — маска payload стартового байта (5/4/3 бита).
	utf8Mask2 = 0x1F
	utf8Mask3 = 0x0F
	utf8Mask4 = 0x07
	// utf8Len2/3/4 — длина UTF-8-символа.
	utf8Len2 = 2
	utf8Len3 = 3
	utf8Len4 = 4
)
