// Package features считает признаки одного цикла пресса — перенос extract_features.py.
//
// Признаки складываются в Features (имя → значение) с ТЕМИ ЖЕ именами, что в Python:
// так Go-признаки можно сверять с parquet от Python и потом подсовывать analyze.py.
// Если признак в Python не ставится (нет канала, нет прессования), здесь его тоже нет.
package features

import (
	"github.com/KudinovArtemilus/press-go/internal/shp"
)

// Features — признаки одного цикла.
type Features map[string]float64

const (
	// PressKN — порог «идёт прессование», кН (PRESS_KN в Python).
	PressKN = 1000.0

	// fallbackDT — шаг, если в файле меньше двух отсчётов (DT в Python).
	fallbackDT = 0.01
)

// stepSeconds — шаг записи файла в секундах, из счётчика.
func stepSeconds(f *shp.File) float64 {
	if s := f.Step().Seconds(); s > 0 {
		return s
	}
	return fallbackDT
}

// Версии признаков — своя у каждой машины. Поднять, когда меняется расчёт признаков машины:
// сервер сам пересчитает её циклы со старой версией (пока файлы в буфере или в архиве).
// Колонка feat_ver; у старых строк её нет — это версия 0.
const (
	PressVersion = 0 // пресс 10: признаки как в Python
	KpsVersion   = 1 // КПС: 1 — признаки с физическим смыслом (kps_*), 21.11.2026
)

// VersionFor — текущая версия признаков машины по типу профиля (config.Profile.Kind).
func VersionFor(kind string) int {
	if kind == "kps" {
		return KpsVersion
	}
	return PressVersion
}

// Compute считает все уже перенесённые признаки цикла.
// Сюда по мере переноса будут добавляться группы: гидростанция, насосы, клапаны...
func Compute(f *shp.File) Features {
	out := Features{}
	dt := stepSeconds(f)
	out["n_samples"] = float64(f.NumSamples())
	out["cycle_s"] = float64(f.NumSamples()) * dt
	out["dt_ms"] = dt * 1000
	Meta(f, out)
	Slide(f, dt, out)
	Hydraulics(f, dt, out)
	Drains(f, out)
	Pumps(f, dt, out)
	Servos(f, dt, out)
	Cylinders(f, out)
	Kps(f, dt, out) // КПС (OP3); у файлов пресса ничего не делает
	RawStats(f, out)
	if IsKps(f) {
		out["feat_ver"] = KpsVersion
	} else {
		out["feat_ver"] = PressVersion
	}
	return out
}
