import { DataTransformerID } from './ids';
import { DataFrame, /*FieldType,*/ Field } from '../../types/dataFrame';
import { DataTransformerInfo } from '../../types/transformations';
import { getFieldDisplayName } from '../../field/fieldState';
import { ArrayVector } from '../../vector/ArrayVector';
import { guessFieldTypeForField } from '../../dataframe/processDataFrame';
import { ReducerID } from '../fieldReducer';

export interface ValueFilter {
  type: string;
  fieldName: string | null; // Corresponding field name
  filterExpression: string | null; // The filter expression / value
}

export interface FilterByValueTransformerOptions {
  valueFilters: [ValueFilter];
}

export const filterByValueTransformer: DataTransformerInfo<FilterByValueTransformerOptions> = {
  id: DataTransformerID.filterByValue,
  name: 'Filter by Value',
  description: 'Filter the data points (rows) depending on the value of certain fields',
  defaultOptions: {
    valueFilters: [{ type: 'include', fieldName: null, filterExpression: null }],
  },

  /**
   * Return a modified copy of the series.  If the transform is not or should not
   * be applied, just return the input series
   */
  transformer: (options: FilterByValueTransformerOptions) => {
    console.log('options:', options);
    // const calculationsByField = options.calculationsByField; //.map((val, index) => ({fieldName: val[0], calculations: val[1]}));

    return (data: DataFrame[]) => {
      if (options.valueFilters.length == 0) return data;

      const processed: DataFrame[] = [];

      let includeThisRow = []; // All data points will be flagged for include (true) or exclude (false)
      let defaultIncludeFlag = options.valueFilters[0].type !== 'include';

      for (let frame of data) {
        for (let filterIndex = 0; filterIndex < options.valueFilters.length; filterIndex++) {
          let filter = options.valueFilters[filterIndex];
          let includeFlag = filter.type === 'include';

          // Find the matching field for this filter
          let field = null;
          for (let f of frame.fields) {
            if (getFieldDisplayName(f) === filter.fieldName) {
              field = f;
              break;
            }
          }

          if (field === null) {
            continue; // No field found for for this filter in this frame, ignore
          }

          for (let row = 0; row < frame.length; row++) {
            // Run the filter on each value
            let re = new RegExp(filter.filterExpression);
            console.log('Testing', field.values.get(row), re, re.test(field.values.get(row)));
            if (re.test(field.values.get(row))) {
              // What if the value is not a string ??? Cast before using the value.
              includeThisRow[row] = includeFlag;
            } else if (filterIndex == 0) {
              includeThisRow[row] = defaultIncludeFlag;
            }
          }
        }

        // Create the skeleton of the new data, copy original field attributes
        let filteredFields: Fields[] = [];
        for (let field of frame.fields) {
          filteredFields.push({
            ...field,
            values: new ArrayVector(),
            configs: {
              ...field.config,
            },
          });
        }

        // Create a copy of the data with the included rows only
        console.log(includeThisRow.length);
        let dataLength = 0;
        for (let row = 0; row < includeThisRow.length; row++) {
          if (includeThisRow[row]) {
            for (let j = 0; j < frame.fields.length; j++) {
              filteredFields[j].values.add(frame.fields[j].values.get(row));
            }
            dataLength++;
          }
        }

        processed.push({
          fields: filteredFields,
          length: dataLength,
        });
      }

      return processed;
    };
  },
};
