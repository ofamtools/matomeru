package work

// SetConvertFault makes a conversion's writes fail (tests only).
func SetConvertFault(f func(table, id string) error) { convertFault = f }
